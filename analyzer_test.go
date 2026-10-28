package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/accessanalyzer"
	aatypes "github.com/aws/aws-sdk-go-v2/service/accessanalyzer/types"
)

// fakeAnalyzer returns one page of findings per document, and FAIL from
// CheckNoNewAccess when the new document has an action the existing one lacks.
type fakeAnalyzer struct {
	findings map[string][]aatypes.ValidatePolicyFinding // by a substring of the document
	pages    int
}

func (f *fakeAnalyzer) ValidatePolicy(_ context.Context, in *accessanalyzer.ValidatePolicyInput, _ ...func(*accessanalyzer.Options)) (*accessanalyzer.ValidatePolicyOutput, error) {
	f.pages++
	out := &accessanalyzer.ValidatePolicyOutput{}
	for k, fs := range f.findings {
		if strings.Contains(aws.ToString(in.PolicyDocument), k) {
			out.Findings = append(out.Findings, fs...)
		}
	}
	return out, nil
}

func (f *fakeAnalyzer) CheckNoNewAccess(_ context.Context, in *accessanalyzer.CheckNoNewAccessInput, _ ...func(*accessanalyzer.Options)) (*accessanalyzer.CheckNoNewAccessOutput, error) {
	have, _ := AllowedActions([]byte(aws.ToString(in.ExistingPolicyDocument)))
	next, _ := AllowedActions([]byte(aws.ToString(in.NewPolicyDocument)))
	for _, a := range next {
		if !Covered(a, have) {
			i := int32(0)
			return &accessanalyzer.CheckNoNewAccessOutput{Result: aatypes.CheckNoNewAccessResultFail, Message: aws.String("The modified permissions grant new access compared to your existing policy."),
				Reasons: []aatypes.ReasonSummary{{Description: aws.String("New access in the statement with index: 0."), StatementIndex: &i}}}, nil
		}
	}
	return &accessanalyzer.CheckNoNewAccessOutput{Result: aatypes.CheckNoNewAccessResultPass}, nil
}

func TestValidatePolicies(t *testing.T) {
	aa := &fakeAnalyzer{findings: map[string][]aatypes.ValidatePolicyFinding{
		"s3:GetObject": {
			{FindingType: aatypes.ValidatePolicyFindingTypeSecurityWarning, IssueCode: aws.String("PASS_ROLE_WITH_STAR_IN_RESOURCE"), FindingDetails: aws.String("Using the iam:PassRole action with wildcards (*) in the resource can be overly permissive."), LearnMoreLink: aws.String("https://example.com/passrole")},
			{FindingType: aatypes.ValidatePolicyFindingTypeSuggestion, IssueCode: aws.String("EMPTY_ARRAY_CONDITION"), FindingDetails: aws.String("x")},
		},
	}}
	var b bytes.Buffer
	bad, err := validatePolicies(context.Background(), aa, []namedDoc{
		{"p", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject"],"Resource":"*"}]}`},
		{"p-2", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["sqs:CreateQueue"],"Resource":"*"}]}`},
	}, &b)
	if err != nil {
		t.Fatal(err)
	}
	if bad != 1 || aa.pages != 2 {
		t.Errorf("bad = %d, pages = %d; want 1 security warning over 2 documents", bad, aa.pages)
	}
	for _, want := range []string{"p: SECURITY_WARNING PASS_ROLE_WITH_STAR_IN_RESOURCE", "  https://example.com/passrole", "p: SUGGESTION EMPTY_ARRAY_CONDITION"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("no %q in\n%s", want, b.String())
		}
	}
}

func TestNoNewAccess(t *testing.T) {
	needed := merged(Documents([]string{"s3:CreateBucket", "sqs:CreateQueue"}, nil))
	deployed := []namedDoc{
		{"p", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:CreateBucket"],"Resource":"*"}]}`},
		{"p-2", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["ec2:*"],"Resource":"*"}]}`},
	}
	var b bytes.Buffer
	n, err := noNewAccess(context.Background(), &fakeAnalyzer{}, deployed, needed, &b)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || !strings.Contains(b.String(), "p-2 grants more than the templates need") || !strings.Contains(b.String(), "statement 0: New access") {
		t.Errorf("n = %d\n%s", n, b.String())
	}
	if strings.Contains(b.String(), "p grants") {
		t.Errorf("p was reported:\n%s", b.String())
	}
}
