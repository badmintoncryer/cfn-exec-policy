package main

import (
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
	tbl, err := LoadTable(false)
	if err != nil {
		t.Fatal(err)
	}
	tpls, err := LoadInputs(paths)
	if err != nil {
		t.Fatal(err)
	}
	return RequiredActions(tbl, tpls)
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
	b, _ := json.Marshal(newDoc(1, []string{"s3:*", "sqs:CreateQueue"}))
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
