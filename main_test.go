package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
)

func generate(t *testing.T, paths ...string) ([]string, []string) {
	t.Helper()
	return generateWith(t, false, paths...)
}

func generateWith(t *testing.T, passCond bool, paths ...string) ([]string, []string) {
	t.Helper()
	tbl, err := LoadTable(false)
	if err != nil {
		t.Fatal(err)
	}
	tpls, err := LoadInputs(paths)
	if err != nil {
		t.Fatal(err)
	}
	a, w, _ := RequiredActions(tbl, tpls, passCond)
	return a, w
}

func TestCdkOut(t *testing.T) {
	actions, warnings := generate(t, "testdata/cdk.out")
	for _, want := range []string{
		"s3:CreateBucket", "s3:DeleteBucket", // handler permissions
		"sqs:CreateQueue",              // nested stack
		"dynamodb:CreateTable",         // stage (nested assembly)
		"greengrass:*", "iam:PassRole", // type without handlers
		"lambda:InvokeFunction",          // custom resource
		"ssm:GetParameters",              // BootstrapVersion parameter
		"secretsmanager:GetSecretValue",  // dynamic reference
		"s3:GetObject",                   // nested template
		"cloudformation:CreateChangeSet", // nested stacks run through change sets
	} {
		if !Covered(want, actions) {
			t.Errorf("missing %s", want)
		}
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "AWS::Greengrass::Group") || !strings.Contains(joined, "Third::Party::Thing") {
		t.Errorf("warnings = %q", warnings)
	}
	if strings.Contains(joined, "AWS::CDK::Metadata") {
		t.Errorf("CDK::Metadata should be ignored: %q", warnings)
	}
}

func TestStackNames(t *testing.T) {
	tpls, err := LoadInputs([]string{"testdata/cdk.out"})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tp := range tpls {
		names[tp.StackName] = true
	}
	for _, n := range []string{"App", "Stage-StageStack", ""} { // "" = nested stack
		if !names[n] {
			t.Errorf("stack %q not loaded; got %v", n, names)
		}
	}
}

func TestYAMLShortForm(t *testing.T) {
	actions, _ := generate(t, "testdata/template.yaml")
	for _, want := range []string{"lambda:CreateFunction", "iam:CreateRole"} {
		if !Covered(want, actions) {
			t.Errorf("missing %s", want)
		}
	}
}

func TestCompact(t *testing.T) {
	got := Compact([]string{"ec2:DescribeA", "ec2:DescribeB", "ec2:CreateX", "ec2:ListA",
		"kms:*", "kms:Decrypt", "s3:GetObject"})
	want := "ec2:CreateX ec2:Describe* ec2:ListA kms:* s3:GetObject"
	if strings.Join(got, " ") != want {
		t.Errorf("got %v want %s", got, want)
	}
}

func TestDocumentsSplitUnderLimit(t *testing.T) {
	var actions []string
	for i := 0; i < 600; i++ {
		actions = append(actions, "service:SomeFairlyLongActionName"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+strings.Repeat("y", i/26))
	}
	docs := Documents(actions, nil)
	if len(docs) < 2 {
		t.Fatalf("expected a split, got %d doc", len(docs))
	}
	n := 0
	for _, d := range docs {
		if s := size(d); s > MaxPolicySize {
			t.Errorf("doc size %d > %d", s, MaxPolicySize)
		}
		n += len(d.Statement[0].Action)
	}
	if n != len(actions) {
		t.Errorf("lost actions: %d of %d", n, len(actions))
	}
}

func TestAllowedActionsRoundTrip(t *testing.T) {
	b, _ := json.Marshal(newDoc(1, []string{"s3:*", "sqs:CreateQueue"}, nil, nil))
	got, err := AllowedActions(b)
	if err != nil || strings.Join(got, ",") != "s3:*,sqs:CreateQueue" {
		t.Fatalf("got %v %v", got, err)
	}
	got, _ = AllowedActions([]byte(`{"Statement":{"Effect":"Allow","Action":"*"}}`))
	if len(got) != 1 || !Covered("anything:Goes", got) {
		t.Fatalf("single statement / string action: %v", got)
	}
}

func TestEmptyDocumentGrantsNothing(t *testing.T) {
	b, _ := json.Marshal(EmptyDocument())
	got, err := AllowedActions(b)
	if err != nil || len(got) != 0 {
		t.Fatalf("EmptyDocument allows %v (err %v)", got, err)
	}
}

func TestHandWritten(t *testing.T) {
	actions, warnings := generate(t, "testdata/handwritten.yaml")
	for _, want := range []string{"codebuild:CreateProject", "iam:PassRole", "route53:ChangeResourceRecordSets"} {
		if !Covered(want, actions) {
			t.Errorf("missing %s", want)
		}
	}
	if Covered("codebuild:StartBuild", actions) {
		t.Errorf("fell back to codebuild:*: %v", actions)
	}
	if len(warnings) > 0 {
		t.Errorf("measured types should not warn: %q", warnings)
	}
}

// A row must go once the schema publishes handlers for the type, or it shadows nothing.
func TestHandWrittenOnlyForTypesWithoutHandlers(t *testing.T) {
	tbl, err := LoadTable(false)
	if err != nil {
		t.Fatal(err)
	}
	for typ := range handWritten {
		if tbl.Types[typ] != nil {
			t.Errorf("%s now has handler permissions in the schema; drop its hand-written row", typ)
		}
	}
}

type fakeCFN struct {
	templates map[string]string
	nested    map[string][]string
}

func (f fakeCFN) GetTemplate(_ context.Context, in *cloudformation.GetTemplateInput, _ ...func(*cloudformation.Options)) (*cloudformation.GetTemplateOutput, error) {
	body, ok := f.templates[*in.StackName]
	if !ok {
		return nil, errors.New("Stack with id " + *in.StackName + " does not exist")
	}
	return &cloudformation.GetTemplateOutput{TemplateBody: &body}, nil
}

func (f fakeCFN) ListStackResources(_ context.Context, in *cloudformation.ListStackResourcesInput, _ ...func(*cloudformation.Options)) (*cloudformation.ListStackResourcesOutput, error) {
	var out []cfntypes.StackResourceSummary
	for _, arn := range f.nested[*in.StackName] {
		out = append(out, cfntypes.StackResourceSummary{ResourceType: aws.String("AWS::CloudFormation::Stack"), PhysicalResourceId: aws.String(arn)})
	}
	return &cloudformation.ListStackResourcesOutput{StackResourceSummaries: out}, nil
}

// A type removed only from a deployed nested stack must keep its delete permissions.
func TestDeployedNestedStacks(t *testing.T) {
	c := &awsClients{cfn: fakeCFN{
		templates: map[string]string{
			"App":         `{"Resources":{"Inner":{"Type":"AWS::CloudFormation::Stack"}}}`,
			"arn:inner":   `{"Resources":{"Inner2":{"Type":"AWS::CloudFormation::Stack"}}}`,
			"arn:inner-2": `{"Resources":{"Topic":{"Type":"AWS::SNS::Topic"}}}`,
		},
		nested: map[string][]string{"App": {"arn:inner"}, "arn:inner": {"arn:inner-2"}},
	}}
	got, err := c.deployedTemplates(context.Background(), []*Template{{StackName: "App"}, {StackName: "NotDeployed"}})
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, tpl := range got {
		for _, r := range tpl.Resources {
			types = append(types, r.Type)
		}
	}
	if !strings.Contains(strings.Join(types, " "), "AWS::SNS::Topic") {
		t.Errorf("nested-of-nested template not fetched: %v", types)
	}
}

func TestBuildTableIncludesTaggingPermissions(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"aws-x-y.json": `{"typeName":"AWS::X::Y","handlers":{"create":{"permissions":["x:CreateY","x:TagResource"]}},
			"tagging":{"taggable":true,"permissions":["x:TagResource","x:UntagResource"]}}`,
		"aws-x-nohandlers.json": `{"typeName":"AWS::X::NoHandlers","tagging":{"permissions":["x:TagResource"]}}`,
	} {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	tbl, err := BuildTable(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !Covered("x:UntagResource", tbl.Types["AWS::X::Y"]) {
		t.Errorf("tagging permissions missing: %v", tbl.Types["AWS::X::Y"])
	}
	if tbl.Types["AWS::X::NoHandlers"] != nil {
		t.Errorf("a type without handlers must stay out of the table (it falls back): %v", tbl.Types["AWS::X::NoHandlers"])
	}
}

func TestPassRoleCondition(t *testing.T) {
	actions, warnings := generateWith(t, true, "testdata/passrole.yaml")
	if len(warnings) > 0 || contains(actions, "iam:PassRole") {
		t.Fatalf("mapped types must not need a bare iam:PassRole: %v %v", warnings, actions)
	}
	docs := Documents(Compact(actions), nil)
	if len(docs) != 1 || len(docs[0].Statement) != 2 {
		t.Fatalf("want one document with a conditioned statement: %+v", docs)
	}
	got := docs[0].Statement[1].Condition["StringEquals"]["iam:PassedToService"]
	if strings.Join(got, ",") != "lambda.amazonaws.com,states.amazonaws.com" {
		t.Fatalf("services: %v", got)
	}
	// check and apply read the condition back, so a policy written with the flag
	// covers what it was written for, and nothing passed to another service.
	b, _ := json.Marshal(docs[0])
	have, _ := AllowedActions(b)
	for _, a := range actions {
		if !Covered(a, have) {
			t.Errorf("round trip lost %s", a)
		}
	}
	if Covered("iam:PassRole", have) || Covered(passRole+"ecs-tasks.amazonaws.com", have) {
		t.Errorf("conditioned grant must not cover other services: %v", have)
	}
	if !Covered(passRole+"lambda.amazonaws.com", []string{"iam:PassRole"}) {
		t.Errorf("a bare iam:PassRole must cover a conditioned need")
	}
	if a, _ := generateWith(t, false, "testdata/passrole.yaml"); !contains(a, "iam:PassRole") {
		t.Errorf("without the flag iam:PassRole stays bare: %v", a)
	}
}

func TestPassRoleConditionUnmappedTypeStaysBare(t *testing.T) {
	actions, warnings := generateWith(t, true, "testdata/passrole.yaml", "testdata/passrole-unmapped.yaml")
	if !contains(actions, "iam:PassRole") || len(warnings) != 1 {
		t.Fatalf("an unmapped type must keep a bare iam:PassRole with a warning: %v %v", warnings, actions)
	}
	for _, d := range Documents(Compact(actions), nil) {
		if len(d.Statement) != 1 {
			t.Fatalf("the bare grant covers the conditioned one; no second statement: %+v", d)
		}
	}
}

func TestStrictErrors(t *testing.T) {
	tpls, err := LoadInputs([]string{"testdata/strict", "testdata/strict-bad.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	errs := strings.Join(StrictErrors(tpls, "b"), "\n")
	for _, want := range []string{"NoBoundary has no PermissionsBoundary", `{"name": "b"}`, "Human (AWS::IAM::User)", "Readers (AWS::IAM::ManagedPolicy)"} {
		if !strings.Contains(errs, want) {
			t.Errorf("missing %q in:\n%s", want, errs)
		}
	}
	for _, not := range []string{"Bounded", "Named", "PlainPolicy"} {
		if strings.Contains(errs, not) {
			t.Errorf("%s flagged:\n%s", not, errs)
		}
	}
}

func TestPassRoleScope(t *testing.T) {
	tpls, err := LoadInputs([]string{"testdata/strict"})
	if err != nil {
		t.Fatal(err)
	}
	roles, warnings, err := PassRoleScope(tpls)
	want := "arn:*:iam::*:role/StrictApp-*,arn:*:iam::*:role/my-fixed-role,arn:*:iam::*:role/service-role/external-role"
	if err != nil || strings.Join(roles, ",") != want || len(warnings) > 0 {
		t.Errorf("got %v %v %v", roles, warnings, err)
	}
	// CloudFormation keeps 25 characters of a long stack name in generated role names.
	long := &Template{StackName: strings.Repeat("a", 30), Path: "x"}
	if roles, _, _ := PassRoleScope([]*Template{long}); roles[0] != "arn:*:iam::*:role/"+strings.Repeat("a", 25)+"*" {
		t.Errorf("long stack name: %v", roles)
	}
	bad, err := LoadInputs([]string{"testdata/strict-bad.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := PassRoleScope(bad); err == nil {
		t.Error("a template file without a stack name should be an error")
	}
	bad[0].StackName = "Bad"
	if roles, warnings, _ := PassRoleScope(bad); len(roles) != 1 || len(warnings) != 1 || !strings.Contains(warnings[0], "RoleName of NoBoundary") {
		t.Errorf("!Ref RoleName should warn, not widen: %v %v", roles, warnings)
	}
}

func TestStrictDocuments(t *testing.T) {
	s := &Strict{Boundary: "b", Roles: []string{"arn:*:iam::*:role/App-*"}}
	b, _ := json.Marshal(Documents([]string{"iam:CreateRole", "iam:GetRole", "iam:PassRole", "iam:PassRole>lambda.amazonaws.com"}, s))
	got := string(b)
	for _, want := range []string{
		`"Action":["iam:GetRole"],"Resource":"*"`,
		`"Action":["iam:CreateRole"],"Resource":"*","Condition":{"StringLike":{"iam:PermissionsBoundary":["arn:*:iam::*:policy/b"]}}`,
		`"Action":["iam:PassRole"],"Resource":["arn:*:iam::*:role/App-*"]}`,
		`"Action":["iam:PassRole"],"Resource":["arn:*:iam::*:role/App-*"],"Condition":{"StringEquals":{"iam:PassedToService":["lambda.amazonaws.com"]}}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if strings.Count(got, "iam:PassRole") != 2 || strings.Count(got, "iam:CreateRole") != 1 {
		t.Errorf("PassRole or CreateRole granted elsewhere: %s", got)
	}
	none, _ := json.Marshal(Documents([]string{"iam:PassRole", "iam:PassRole>lambda.amazonaws.com", "s3:GetObject"}, &Strict{Boundary: "b"}))
	if strings.Contains(string(none), "PassRole") {
		t.Errorf("no roles to pass, yet: %s", none)
	}
}

func TestBoundaryName(t *testing.T) {
	for in, want := range map[string]string{"b": "b", "arn:aws:iam::111111111111:policy/team/b": "team/b"} {
		if got, err := boundaryName(in, "111111111111"); got != want || err != nil {
			t.Errorf("%s: %s %v", in, got, err)
		}
	}
	for _, in := range []string{"arn:aws:iam::222222222222:policy/b", "arn:aws:s3:::b"} {
		if _, err := boundaryName(in, "111111111111"); err == nil {
			t.Errorf("%s accepted", in)
		}
	}
}

func TestGuessedTypes(t *testing.T) {
	tbl, err := LoadTable(false)
	if err != nil {
		t.Fatal(err)
	}
	tpls, err := LoadInputs([]string{"testdata/cdk.out"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, guessed := RequiredActions(tbl, tpls, false); strings.Join(guessed, ",") != "AWS::Greengrass::Group,Third::Party::Thing" {
		t.Errorf("guessed %v", guessed)
	}
}

func TestBoundaryProtectsOnlyThisToolsPolicies(t *testing.T) {
	b, _ := json.Marshal(BoundaryDocument("aws", "1", "p-boundary", "p"))
	got := string(b)
	for _, want := range []string{`"arn:aws:iam::1:policy/p"`, `"arn:aws:iam::1:policy/p-?"`, `"arn:aws:iam::1:policy/p-boundary"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s", want)
		}
	}
	// A stack named p-app creates policies named p-app-Policy-…; the boundary must not freeze them.
	if strings.Contains(got, `policy/p*"`) {
		t.Errorf("prefix wildcard on the policy name: %s", got)
	}
}
