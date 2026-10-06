package main

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

// MaxPolicySize is IAM's managed policy size limit (whitespace excluded).
const MaxPolicySize = 6144

// RequiredActions returns the actions the execution role needs to create, update
// and delete every resource in the templates, plus warnings for guesses.
func RequiredActions(tbl *Table, tpls []*Template) (actions []string, warnings []string) {
	set := map[string]bool{}
	add := func(as ...string) {
		for _, a := range as {
			set[a] = true
		}
	}
	warned := map[string]bool{}
	warn := func(format string, args ...any) {
		w := fmt.Sprintf(format, args...)
		if !warned[w] {
			warned[w] = true
			warnings = append(warnings, w)
		}
	}
	for _, t := range tpls {
		for _, r := range t.Resources {
			switch {
			case r.Type == "AWS::CDK::Metadata":
			case strings.HasPrefix(r.Type, "Custom::") || r.Type == "AWS::CloudFormation::CustomResource":
				// CloudFormation invokes the ServiceToken with the execution role (verified on bench, #3).
				add("lambda:InvokeFunction", "sns:Publish")
			case tbl.Types[r.Type] != nil:
				add(tbl.Types[r.Type]...)
			case measured(r.Type):
				add(handWritten[r.Type]...)
			case tbl.Prefixes[namespace(r.Type)] != "":
				p := tbl.Prefixes[namespace(r.Type)]
				add(p+":*", "iam:PassRole")
				warn("%s has no handler permissions in the schema; granting %s:* and iam:PassRole", r.Type, p)
			default:
				warn("%s is unknown; no permissions granted for it", r.Type)
			}
			if r.Type == "AWS::CloudFormation::Stack" {
				// The nested template URL, and the change sets CloudFormation drives nested
				// stacks through (not in the schema; found on bench, #1).
				add("s3:GetObject", "cloudformation:CreateChangeSet", "cloudformation:DescribeChangeSet",
					"cloudformation:ExecuteChangeSet", "cloudformation:DeleteChangeSet")
			}
		}
		for _, p := range t.Parameters {
			if strings.HasPrefix(p.Type, "AWS::SSM::Parameter::Value") {
				add("ssm:GetParameters")
			}
		}
		raw := string(t.Raw)
		if strings.Contains(raw, "{{resolve:ssm") {
			add("ssm:GetParameters")
		}
		if strings.Contains(raw, "{{resolve:ssm-secure") {
			add("kms:Decrypt")
		}
		if strings.Contains(raw, "{{resolve:secretsmanager") {
			add("secretsmanager:GetSecretValue", "kms:Decrypt")
		}
	}
	return sortedKeys(set), warnings
}

// Compact merges Describe*/List* actions per service and drops actions covered
// by a service wildcard, to keep the policy under the size limit.
func Compact(actions []string) []string {
	bySvc := map[string][]string{}
	for _, a := range actions {
		svc, _, _ := strings.Cut(a, ":")
		bySvc[svc] = append(bySvc[svc], a)
	}
	set := map[string]bool{}
	for svc, as := range bySvc {
		if contains(as, svc+":*") {
			set[svc+":*"] = true
			continue
		}
		for _, verb := range []string{"Describe", "List"} {
			n := 0
			for _, a := range as {
				if strings.HasPrefix(a, svc+":"+verb) {
					n++
				}
			}
			if n >= 2 {
				set[svc+":"+verb+"*"] = true
			}
		}
		for _, a := range as {
			if !Covered(a, sortedKeys(set)) {
				set[a] = true
			}
		}
	}
	return sortedKeys(set)
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// Covered reports whether action is matched by any of the granted patterns.
func Covered(action string, granted []string) bool {
	a := strings.ToLower(action)
	for _, g := range granted {
		if ok, _ := path.Match(strings.ToLower(g), a); ok {
			return true
		}
	}
	return false
}

// PolicyDocument is an IAM policy document.
type PolicyDocument struct {
	Version   string      `json:"Version"`
	Statement []Statement `json:"Statement"`
}

// handWritten covers types whose schema lists no handler permissions. Rows are the
// calls CloudFormation made (CloudTrail, invokedBy cloudformation.amazonaws.com)
// while creating, updating and deleting the type on bench, plus iam:PassRole where
// the type takes a role ARN (CloudTrail never shows it). An empty row means the
// type needs nothing. CloudTrail event names are mapped to IAM actions where they
// differ (Budgets). Dynamic references are left out: RequiredActions adds those from
// the template. Measured 2026-10-06 (#5).
var handWritten = map[string][]string{
	"AWS::AppSync::GraphQLSchema":              {"appsync:GetSchemaCreationStatus", "appsync:StartSchemaCreation"},
	"AWS::CloudFormation::WaitConditionHandle": {},
	"AWS::CloudWatch::AnomalyDetector":         {"cloudwatch:DeleteAnomalyDetector", "cloudwatch:PutAnomalyDetector"},
	"AWS::CodeBuild::Project":                  {"codebuild:CreateProject", "codebuild:DeleteProject", "codebuild:UpdateProject", "iam:PassRole"},
	"AWS::CodeBuild::ReportGroup": {"codebuild:BatchGetReportGroups", "codebuild:CreateReportGroup",
		"codebuild:DeleteReportGroup", "codebuild:UpdateReportGroup"},
	"AWS::IAM::UserToGroupAddition": {"iam:AddUserToGroup", "iam:RemoveUserFromGroup"},
	"AWS::Budgets::Budget":          {"budgets:ModifyBudget", "budgets:ViewBudget"},
	"AWS::CloudFormation::Macro":    {"iam:PassRole"},
	"AWS::DAX::SubnetGroup":         {"dax:CreateSubnetGroup", "dax:DeleteSubnetGroup", "dax:UpdateSubnetGroup"},
	"AWS::DocDB::DBCluster": {"rds:AddTagsToResource", "rds:CreateDBCluster", "rds:DeleteDBCluster",
		"rds:DescribeDBClusters", "rds:ModifyDBCluster", "rds:RemoveTagsFromResource"},
	"AWS::ElasticLoadBalancingV2::ListenerCertificate": {"elasticloadbalancing:AddListenerCertificates", "elasticloadbalancing:RemoveListenerCertificates"},
	"AWS::LakeFormation::Resource": {"iam:GetRole", "iam:PassRole", "lakeformation:DeregisterResource", "lakeformation:RegisterResource",
		"lakeformation:UpdateResource"},
	"AWS::MediaLive::Input": {"iam:PassRole", "medialive:CreateInput", "medialive:CreateTags", "medialive:DeleteInput",
		"medialive:DeleteTags", "medialive:DescribeInput", "medialive:UpdateInput"},
	"AWS::MediaLive::InputSecurityGroup": {"medialive:CreateInputSecurityGroup", "medialive:DeleteInputSecurityGroup"},
	"AWS::Glue::Table":                   {"glue:CreateTable", "glue:DeleteTable", "glue:UpdateTable"},
	"AWS::Route53::RecordSetGroup":       {"route53:ChangeResourceRecordSets", "route53:GetChange", "route53:GetHostedZone"},
}

func measured(typ string) bool {
	_, ok := handWritten[typ]
	return ok
}

// EmptyDocument grants nothing. It replaces a split part that is no longer needed,
// since IAM has no empty policy and the part may still be attached.
func EmptyDocument() PolicyDocument {
	return PolicyDocument{Version: "2012-10-17", Statement: []Statement{
		{Sid: "CfnExecPolicyUnused", Effect: "Deny", Action: []string{"none:null"}, Resource: "*"},
	}}
}

// Statement is an Allow statement on all resources.
type Statement struct {
	Sid      string   `json:"Sid"`
	Effect   string   `json:"Effect"`
	Action   []string `json:"Action"`
	Resource string   `json:"Resource"`
}

func newDoc(i int, actions []string) PolicyDocument {
	return PolicyDocument{Version: "2012-10-17", Statement: []Statement{{
		Sid: fmt.Sprintf("CfnExecPolicy%d", i), Effect: "Allow", Action: actions, Resource: "*",
	}}}
}

func size(d PolicyDocument) int {
	b, _ := json.Marshal(d)
	return len(b)
}

// Documents splits actions into as few policy documents as fit the size limit.
func Documents(actions []string) []PolicyDocument {
	sort.Strings(actions)
	var docs []PolicyDocument
	var cur []string
	for _, a := range actions {
		if len(cur) > 0 && size(newDoc(len(docs)+1, append(append([]string{}, cur...), a))) > MaxPolicySize {
			docs = append(docs, newDoc(len(docs)+1, cur))
			cur = nil
		}
		cur = append(cur, a)
	}
	if len(cur) > 0 {
		docs = append(docs, newDoc(len(docs)+1, cur))
	}
	return docs
}

// AllowedActions extracts Allow actions from a policy document.
func AllowedActions(doc []byte) ([]string, error) {
	var d struct {
		Statement json.RawMessage `json:"Statement"`
	}
	if err := json.Unmarshal(doc, &d); err != nil {
		return nil, err
	}
	var stmts []map[string]any
	if err := json.Unmarshal(d.Statement, &stmts); err != nil {
		var one map[string]any
		if err := json.Unmarshal(d.Statement, &one); err != nil {
			return nil, err
		}
		stmts = []map[string]any{one}
	}
	var out []string
	for _, s := range stmts {
		if s["Effect"] != "Allow" {
			continue
		}
		switch a := s["Action"].(type) {
		case string:
			out = append(out, a)
		case []any:
			for _, x := range a {
				if str, ok := x.(string); ok {
					out = append(out, str)
				}
			}
		}
	}
	return out, nil
}
