package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// SQS is limited to the stacks' generated names, fixed names and literal queue
// URLs; SNS stays on every resource because a subscription imports its topic.
func TestResourceScopes(t *testing.T) {
	tbl, err := LoadTable(false)
	if err != nil {
		t.Fatal(err)
	}
	tpls, err := LoadInputs([]string{"testdata/scope"})
	if err != nil {
		t.Fatal(err)
	}
	scopes, warnings, err := ResourceScopes(tbl, tpls)
	if err != nil {
		t.Fatal(err)
	}
	want := "arn:*:sqs:*:*:AVeryLongStackNameThatGet*,arn:*:sqs:*:*:Messaging-*,arn:*:sqs:*:*:orders.fifo,arn:*:sqs:*:111111111111:shared"
	if got := strings.Join(scopes["sqs"], ","); got != want {
		t.Errorf("sqs:\n got %s\nwant %s", got, want)
	}
	if _, ok := scopes["sns"]; ok || !strings.Contains(strings.Join(warnings, "\n"), "TopicArn of Sub points at a sns resource outside this template") {
		t.Errorf("sns scoped despite an imported topic: %v %v", scopes["sns"], warnings)
	}

	resourceScopes = scopes
	defer func() { resourceScopes = nil }()
	actions, _, _ := RequiredActions(tbl, tpls, false)
	b, _ := json.Marshal(Documents(Compact(actions), nil))
	doc := string(b)
	if !strings.Contains(doc, `"Sid":"CfnExecPolicyScopedSqs1"`) || !strings.Contains(doc, `"arn:*:sqs:*:*:Messaging-*"`) {
		t.Errorf("no scoped SQS statement:\n%s", doc)
	}
	sc, everywhere, err := ScopedResources(b[1 : len(b)-1])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(sc["sqs"], ",") != want || strings.Join(everywhere, ",") != "sns" {
		t.Errorf("read back: %v, everywhere %v", sc, everywhere)
	}

	// A plain template has no stack name to scope by.
	plain, _ := LoadInputs([]string{"testdata/template.yaml"})
	if _, _, err := ResourceScopes(tbl, plain); err == nil {
		t.Error("plain template: no error")
	}
}
