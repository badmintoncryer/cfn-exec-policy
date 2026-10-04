package main

import (
	"encoding/json"
	"strings"
	"testing"
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
		"sqs:CreateQueue",             // nested stack
		"dynamodb:CreateTable",        // stage (nested assembly)
		"codebuild:*", "iam:PassRole", // type without handlers
		"lambda:InvokeFunction",         // custom resource
		"ssm:GetParameters",             // BootstrapVersion parameter
		"secretsmanager:GetSecretValue", // dynamic reference
		"s3:GetObject",                  // nested template
	} {
		if !Covered(want, actions) {
			t.Errorf("missing %s", want)
		}
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "AWS::CodeBuild::Project") || !strings.Contains(joined, "Third::Party::Thing") {
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
