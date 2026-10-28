package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func diffOf(t *testing.T, passCond bool, base, head string) Diff {
	t.Helper()
	tbl, err := LoadTable(false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadInputs([]string{base})
	if err != nil {
		t.Fatal(err)
	}
	h, err := LoadInputs([]string{head})
	if err != nil {
		t.Fatal(err)
	}
	return DiffPolicies(tbl, b, h, passCond)
}

func change(d Diff, typ string) *TypeChange {
	for _, cs := range [][]TypeChange{d.Added, d.Removed} {
		for i := range cs {
			if cs[i].Type == typ {
				return &cs[i]
			}
		}
	}
	return nil
}

func TestDiff(t *testing.T) {
	d := diffOf(t, false, "testdata/diff-base.yaml", "testdata/diff-head.yaml")
	if q := change(d, "AWS::SQS::Queue"); q == nil || !q.New || !slices.Contains(q.Actions, "sqs:CreateQueue") || len(q.Risks) != 0 {
		t.Errorf("SQS queue: %+v", q)
	}
	if m := change(d, "AWS::AppMesh::Mesh"); m == nil || !slices.Equal(m.Risks, []string{riskWildcard}) || !slices.Contains(m.Actions, "appmesh:*") {
		t.Errorf("AppMesh mesh: %+v", m)
	}
	if u := change(d, "Third::Party::Thing"); u == nil || !slices.Equal(u.Risks, []string{riskUnknown}) {
		t.Errorf("unknown type: %+v", u)
	}
	if f := change(d, "AWS::Lambda::Function"); f != nil {
		t.Errorf("unchanged Lambda function listed: %+v", f)
	}
	if s := change(d, "AWS::SNS::Topic"); s == nil || !s.Gone || !slices.Contains(s.Actions, "sns:CreateTopic") {
		t.Errorf("SNS topic: %+v", s)
	}
	if !slices.Contains(d.Dropped, "sns:CreateTopic") || slices.Contains(d.Dropped, "lambda:CreateFunction") {
		t.Errorf("dropped = %v", d.Dropped)
	}
	// The Lambda function in the base already needs iam:PassRole, so the AppMesh fallback adds no PassRole risk.
	if !slices.Equal(d.Risks, []string{riskUnknown, riskWildcard}) {
		t.Errorf("risks = %v", d.Risks)
	}
	if got := d.failed([]string{riskPassRole, riskWildcard, "any"}); !slices.Equal(got, []string{riskWildcard, "any"}) {
		t.Errorf("failed = %v", got)
	}
}

func TestDiffNoChange(t *testing.T) {
	d := diffOf(t, false, "testdata/diff-base.yaml", "testdata/diff-base.yaml")
	if !d.Empty() || d.failed([]string{"any"}) != nil {
		t.Errorf("diff = %+v", d)
	}
	var buf bytes.Buffer
	d.WriteMarkdown(&buf)
	if !strings.HasPrefix(buf.String(), "### Execution policy: no change") {
		t.Errorf("md = %q", buf.String())
	}
}

func TestDiffPassRole(t *testing.T) {
	// A Lambda function brings the first iam:PassRole, unconditioned by default.
	d := diffOf(t, false, "testdata/diff-queue.yaml", "testdata/template.yaml")
	if f := change(d, "AWS::Lambda::Function"); f == nil || !slices.Equal(f.Risks, []string{riskPassRole}) {
		t.Errorf("function without the condition: %+v", f)
	}
	// With the condition, PassRole is a token for its service and not a risk.
	d = diffOf(t, true, "testdata/diff-queue.yaml", "testdata/passrole.yaml")
	f := change(d, "AWS::Lambda::Function")
	if f == nil || !slices.Contains(f.Actions, passRole+"lambda.amazonaws.com") || len(f.Risks) != 0 {
		t.Errorf("function with the condition: %+v", f)
	}
	var buf bytes.Buffer
	d.WriteText(&buf)
	if !strings.Contains(buf.String(), "iam:PassRole to lambda.amazonaws.com") {
		t.Errorf("text = %s", buf.String())
	}
}

func TestDiffMarkdown(t *testing.T) {
	d := diffOf(t, false, "testdata/diff-base.yaml", "testdata/diff-head.yaml")
	var buf bytes.Buffer
	d.WriteMarkdown(&buf)
	md := buf.String()
	for _, want := range []string{
		"### Execution policy: +",
		"> [!WARNING]\n> Grants every action of the service: the type has no measured permissions: `AWS::AppMesh::Mesh`\n",
		"| added | `AWS::SQS::Queue` | `sqs:CreateQueue` ",
		"| added | `Third::Party::Thing` | none: unknown type |",
		"| removed | `AWS::SNS::Topic` | <details><summary>",
		"no longer needed; `apply` keeps them unless `--prune`.",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("md has no %q:\n%s", want, md)
		}
	}
}
