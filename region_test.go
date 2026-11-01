package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestRequestedRegions(t *testing.T) {
	tpls, err := LoadInputs([]string{"testdata/cdk.out"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := RequestedRegions(tpls, "ap-northeast-1")
	if err != nil {
		t.Fatal(err)
	}
	// us-east-1 always, for the global services; env-agnostic stacks fall back.
	if !slices.Contains(got, "us-east-1") || !slices.Contains(got, "ap-northeast-1") {
		t.Errorf("regions = %v", got)
	}
	if _, err := RequestedRegions([]*Template{{Path: "app.yaml"}}, ""); err == nil {
		t.Error("a template with no region and no fallback: no error")
	}
	nested := []*Template{{Path: "cdk.out/AppNested.nested.template.json"}, {Path: "deployed:App", StackName: "App"}}
	if got, err := RequestedRegions(nested, ""); err != nil || !slices.Equal(got, []string{"us-east-1"}) {
		t.Errorf("nested and deployed templates: %v %v", got, err)
	}
}

func TestRegionCondition(t *testing.T) {
	regions := []string{"eu-west-1", "us-east-1"}
	docs := DocumentsIn([]string{"s3:CreateBucket", "iam:CreateRole", "iam:PassRole", passRole + "lambda.amazonaws.com"},
		&Strict{Boundary: "b", Roles: []string{"arn:*:iam::*:role/App-*"}}, regions)
	b, _ := json.Marshal(docs[0])
	for _, st := range docs[0].Statement {
		if !slices.Equal(st.Condition["StringEquals"][requestedRegion], regions) {
			t.Errorf("%s has no region condition: %s", st.Sid, b)
		}
	}
	// The conditions the other flags write are kept beside it.
	if !strings.Contains(string(b), `"iam:PassedToService":["lambda.amazonaws.com"]`) || !strings.Contains(string(b), `"iam:PermissionsBoundary"`) {
		t.Errorf("doc = %s", b)
	}
	// Reading the policy back: actions (PassRole tokens too), roles and regions.
	got, err := AllowedActions(b)
	if err != nil || !slices.Contains(got, passRole+"lambda.amazonaws.com") || !slices.Contains(got, "s3:CreateBucket") {
		t.Errorf("actions = %v %v", got, err)
	}
	if regs, err := DocRegions(b); err != nil || !slices.Equal(regs, regions) {
		t.Errorf("regions = %v %v", regs, err)
	}
	plain, _ := json.Marshal(Documents([]string{"s3:CreateBucket"}, nil)[0])
	if regs, err := DocRegions(plain); err != nil || regs != nil {
		t.Errorf("a policy without the condition: %v %v", regs, err)
	}
}
