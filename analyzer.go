package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/accessanalyzer"
	aatypes "github.com/aws/aws-sdk-go-v2/service/accessanalyzer/types"
)

// analyzerAPI is the part of the IAM Access Analyzer client used here (a fake in tests).
type analyzerAPI interface {
	ValidatePolicy(context.Context, *accessanalyzer.ValidatePolicyInput, ...func(*accessanalyzer.Options)) (*accessanalyzer.ValidatePolicyOutput, error)
	CheckNoNewAccess(context.Context, *accessanalyzer.CheckNoNewAccessInput, ...func(*accessanalyzer.Options)) (*accessanalyzer.CheckNoNewAccessOutput, error)
}

// namedDoc is a policy document and the name to report it by.
type namedDoc struct {
	name string
	doc  string
}

// validatePolicies runs each document through accessanalyzer:ValidatePolicy,
// prints the findings to w and returns how many are errors or security
// warnings (the ones check fails on).
func validatePolicies(ctx context.Context, aa analyzerAPI, docs []namedDoc, w io.Writer) (int, error) {
	bad := 0
	for _, d := range docs {
		pg := accessanalyzer.NewValidatePolicyPaginator(aa, &accessanalyzer.ValidatePolicyInput{
			PolicyDocument: aws.String(d.doc), PolicyType: aatypes.PolicyTypeIdentityPolicy,
		})
		for pg.HasMorePages() {
			out, err := pg.NextPage(ctx)
			if err != nil {
				return 0, fmt.Errorf("ValidatePolicy %s: %w", d.name, err)
			}
			for _, f := range out.Findings {
				if f.FindingType == aatypes.ValidatePolicyFindingTypeError || f.FindingType == aatypes.ValidatePolicyFindingTypeSecurityWarning {
					bad++
				}
				fmt.Fprintf(w, "%s: %s %s: %s\n", d.name, f.FindingType, aws.ToString(f.IssueCode), aws.ToString(f.FindingDetails))
				if l := aws.ToString(f.LearnMoreLink); l != "" {
					fmt.Fprintf(w, "  %s\n", l)
				}
			}
		}
	}
	return bad, nil
}

// noNewAccess checks with accessanalyzer:CheckNoNewAccess that no deployed
// document grants more than needed, the policy the templates need, and prints
// what it does grant beyond it. It returns how many documents do.
func noNewAccess(ctx context.Context, aa analyzerAPI, deployed []namedDoc, needed PolicyDocument, w io.Writer) (int, error) {
	b, err := json.Marshal(needed)
	if err != nil {
		return 0, err
	}
	broader := 0
	for _, d := range deployed {
		out, err := aa.CheckNoNewAccess(ctx, &accessanalyzer.CheckNoNewAccessInput{
			NewPolicyDocument: aws.String(d.doc), ExistingPolicyDocument: aws.String(string(b)),
			PolicyType: aatypes.AccessCheckPolicyTypeIdentityPolicy,
		})
		if err != nil {
			return 0, fmt.Errorf("CheckNoNewAccess %s: %w", d.name, err)
		}
		if out.Result == aatypes.CheckNoNewAccessResultPass {
			continue
		}
		broader++
		fmt.Fprintf(w, "%s grants more than the templates need: %s\n", d.name, aws.ToString(out.Message))
		for _, r := range out.Reasons {
			where := aws.ToString(r.StatementId)
			if where == "" && r.StatementIndex != nil {
				where = fmt.Sprintf("statement %d", *r.StatementIndex)
			}
			fmt.Fprintf(w, "  %s: %s\n", where, aws.ToString(r.Description))
		}
	}
	return broader, nil
}

// merged puts the statements of all parts into one document, the reference
// for CheckNoNewAccess.
func merged(docs []PolicyDocument) PolicyDocument {
	out := PolicyDocument{Version: "2012-10-17"}
	for _, d := range docs {
		out.Statement = append(out.Statement, d.Statement...)
	}
	return out
}
