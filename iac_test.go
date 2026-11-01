package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestIdentifier(t *testing.T) {
	for name, want := range map[string][2]string{
		"cfn-exec-policy":   {"cfn_exec_policy", "CfnExecPolicy"},
		"cfn-exec-policy-2": {"cfn_exec_policy_2", "CfnExecPolicy2"},
		"2nd.policy":        {"p2nd_policy", "p2ndPolicy"},
	} {
		if got := identifier(name, false); got != want[0] {
			t.Errorf("terraform %s = %s, want %s", name, got, want[0])
		}
		if got := identifier(name, true); got != want[1] {
			t.Errorf("cdk %s = %s, want %s", name, got, want[1])
		}
	}
}

func TestIaC(t *testing.T) {
	docs := []PolicyDocument{
		{Version: "2012-10-17", Statement: []Statement{{Sid: "A", Effect: "Allow", Action: []string{"s3:*"}, Resource: "arn:aws:s3:::${aws:username}-*"}}},
		{Version: "2012-10-17", Statement: []Statement{{Sid: "B", Effect: "Allow", Action: []string{"sqs:*"}, Resource: "*"}}},
	}
	names := policyNames("cfn-exec-policy", 2)
	var b bytes.Buffer
	WriteTerraform(&b, names, docs)
	tf := b.String()
	for _, want := range []string{
		`resource "aws_iam_policy" "cfn_exec_policy" {`, `name   = "cfn-exec-policy-2"`,
		`"Resource": "arn:aws:s3:::$${aws:username}-*"`, // not interpolated by the heredoc
		`value = [aws_iam_policy.cfn_exec_policy.arn, aws_iam_policy.cfn_exec_policy_2.arn]`,
	} {
		if !strings.Contains(tf, want) {
			t.Errorf("terraform: no %q in\n%s", want, tf)
		}
	}
	b.Reset()
	WriteCDK(&b, names, docs)
	cdk := b.String()
	for _, want := range []string{
		`new iam.ManagedPolicy(scope, "CfnExecPolicy2", {`, `managedPolicyName: "cfn-exec-policy",`,
		`document: iam.PolicyDocument.fromJson({`, `"Resource": "arn:aws:s3:::${aws:username}-*"`,
	} {
		if !strings.Contains(cdk, want) {
			t.Errorf("cdk: no %q in\n%s", want, cdk)
		}
	}
}
