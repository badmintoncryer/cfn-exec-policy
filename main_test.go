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
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
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
	return RequiredActions(tbl, tpls, passCond)
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
	docs := Documents(actions)
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
	b, _ := json.Marshal(newDoc(1, []string{"s3:*", "sqs:CreateQueue"}, nil))
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
	docs := Documents(Compact(actions))
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
	for _, d := range Documents(Compact(actions)) {
		if len(d.Statement) != 1 {
			t.Fatalf("the bare grant covers the conditioned one; no second statement: %+v", d)
		}
	}
}

// fakeIAM holds the version IDs of one policy and, like IAM, refuses to delete it
// while non-default versions remain.
type fakeIAM struct {
	iamAPI
	versions []string // versions[0] is the default
	roles    []string
	deleted  bool
}

func (f *fakeIAM) ListPolicyVersions(_ context.Context, _ *iam.ListPolicyVersionsInput, _ ...func(*iam.Options)) (*iam.ListPolicyVersionsOutput, error) {
	var out []iamtypes.PolicyVersion
	for i, v := range f.versions {
		out = append(out, iamtypes.PolicyVersion{VersionId: aws.String(v), IsDefaultVersion: i == 0})
	}
	return &iam.ListPolicyVersionsOutput{Versions: out}, nil
}

func (f *fakeIAM) DeletePolicyVersion(_ context.Context, in *iam.DeletePolicyVersionInput, _ ...func(*iam.Options)) (*iam.DeletePolicyVersionOutput, error) {
	for i, v := range f.versions {
		if i > 0 && v == aws.ToString(in.VersionId) {
			f.versions = append(f.versions[:i], f.versions[i+1:]...)
			return &iam.DeletePolicyVersionOutput{}, nil
		}
	}
	return nil, errors.New("no such non-default version")
}

func (f *fakeIAM) DeletePolicy(context.Context, *iam.DeletePolicyInput, ...func(*iam.Options)) (*iam.DeletePolicyOutput, error) {
	if len(f.versions) > 1 || len(f.roles) > 0 {
		return nil, &iamtypes.DeleteConflictException{}
	}
	f.deleted = true
	return &iam.DeletePolicyOutput{}, nil
}

func (f *fakeIAM) ListEntitiesForPolicy(context.Context, *iam.ListEntitiesForPolicyInput, ...func(*iam.Options)) (*iam.ListEntitiesForPolicyOutput, error) {
	var out []iamtypes.PolicyRole
	for _, r := range f.roles {
		out = append(out, iamtypes.PolicyRole{RoleName: aws.String(r)})
	}
	return &iam.ListEntitiesForPolicyOutput{PolicyRoles: out}, nil
}

func TestDeletePolicy(t *testing.T) {
	ctx := context.Background()
	f := &fakeIAM{versions: []string{"v3", "v1", "v2"}, roles: []string{"exec"}}
	c := &awsClients{iam: f, part: "aws", account: "123456789012"}
	if got, _ := c.attachedTo(ctx, "p"); strings.Join(got, ",") != "role/exec" {
		t.Errorf("attachedTo = %v", got)
	}
	f.roles = nil
	if err := c.deletePolicy(ctx, "p"); err != nil || !f.deleted {
		t.Fatalf("deletePolicy: err=%v deleted=%v versions=%v", err, f.deleted, f.versions)
	}
}
