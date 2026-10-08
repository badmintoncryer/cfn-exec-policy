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
// and delete every resource in the templates, plus warnings for guesses. With
// passCond, iam:PassRole of a type in passedToService becomes a passRole token.
func RequiredActions(tbl *Table, tpls []*Template, passCond bool) (actions []string, warnings []string) {
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
			var as []string
			switch {
			case r.Type == "AWS::CDK::Metadata":
			case strings.HasPrefix(r.Type, "Custom::") || r.Type == "AWS::CloudFormation::CustomResource":
				// CloudFormation invokes the ServiceToken with the execution role (verified on bench, #3).
				as = []string{"lambda:InvokeFunction", "sns:Publish"}
			case tbl.Types[r.Type] != nil:
				as = tbl.Types[r.Type]
			case measured(r.Type):
				as = handWritten[r.Type]
			case tbl.Prefixes[namespace(r.Type)] != "":
				p := tbl.Prefixes[namespace(r.Type)]
				as = []string{p + ":*", "iam:PassRole"}
				warn("%s has no handler permissions in the schema; granting %s:* and iam:PassRole", r.Type, p)
			default:
				warn("%s is unknown; no permissions granted for it", r.Type)
			}
			for _, a := range as {
				switch {
				case a != "iam:PassRole" || !passCond:
					add(a)
				case passedToService[r.Type] != nil:
					for _, svc := range passedToService[r.Type] {
						add(passRole + svc)
					}
				default:
					add(a)
					warn("%s is not in the --pass-role-condition map; iam:PassRole stays unconditioned", r.Type)
				}
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
// A passRole token is also covered by whatever covers iam:PassRole itself.
func Covered(action string, granted []string) bool {
	a := strings.ToLower(action)
	plain, _, _ := strings.Cut(a, ">")
	for _, g := range granted {
		g = strings.ToLower(g)
		if ok, _ := path.Match(g, a); ok {
			return true
		}
		if ok, _ := path.Match(g, plain); ok && !strings.Contains(g, ">") {
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
// type needs nothing. Authorization-only checks never show up in CloudTrail either
// (EC2 tag-on-create needs ec2:CreateTags); the verify pass catches those. CloudTrail event names are mapped to IAM actions where they
// differ (Budgets). Dynamic references are left out: RequiredActions adds those from
// the template. Tag actions for Budgets and MediaLive::InputSecurityGroup come from
// the IAM docs (bench templates did not change tags in place). Measured 2026-10-06 (#5).
var handWritten = map[string][]string{
	"AWS::AppSync::GraphQLSchema":              {"appsync:GetSchemaCreationStatus", "appsync:StartSchemaCreation"},
	"AWS::CloudFormation::WaitConditionHandle": {},
	"AWS::CodeBuild::Project":                  {"codebuild:CreateProject", "codebuild:DeleteProject", "codebuild:UpdateProject", "iam:PassRole"},
	"AWS::CodeBuild::ReportGroup": {"codebuild:BatchGetReportGroups", "codebuild:CreateReportGroup",
		"codebuild:DeleteReportGroup", "codebuild:UpdateReportGroup"},
	"AWS::IAM::UserToGroupAddition": {"iam:AddUserToGroup", "iam:RemoveUserFromGroup"},
	// The plan creates the scaling policies and alarms with the caller's credentials (the
	// autoscaling/cloudwatch actions, from the verify pass's errors and CloudTrail AccessDenied). Measured with an EC2
	// Auto Scaling group only; other namespaces would also need application-autoscaling.
	// The first plan creates the AutoScalingPlans service-linked role; bench already had it
	// after measuring, so iam:CreateServiceLinkedRole comes from the service docs.
	"AWS::AutoScalingPlans::ScalingPlan": {"autoscaling-plans:CreateScalingPlan", "autoscaling-plans:DeleteScalingPlan",
		"autoscaling-plans:DescribeScalingPlans", "autoscaling-plans:UpdateScalingPlan", "autoscaling:DeletePolicy",
		"autoscaling:DescribeAutoScalingGroups", "autoscaling:DescribePolicies", "autoscaling:PutScalingPolicy", "autoscaling:UpdateAutoScalingGroup",
		"cloudwatch:DeleteAlarms", "cloudwatch:DescribeAlarms", "cloudwatch:PutMetricAlarm", "iam:CreateServiceLinkedRole"},
	"AWS::Budgets::Budget":       {"budgets:ListTagsForResource", "budgets:ModifyBudget", "budgets:TagResource", "budgets:UntagResource", "budgets:ViewBudget"},
	"AWS::CloudFormation::Macro": {"iam:PassRole"},
	"AWS::DAX::SubnetGroup":      {"dax:CreateSubnetGroup", "dax:DeleteSubnetGroup", "dax:UpdateSubnetGroup"},
	"AWS::DocDB::DBCluster": {"rds:AddTagsToResource", "rds:CreateDBCluster", "rds:DeleteDBCluster",
		"rds:DescribeDBClusters", "rds:ModifyDBCluster", "rds:RemoveTagsFromResource"},
	"AWS::ElasticLoadBalancingV2::ListenerCertificate": {"elasticloadbalancing:AddListenerCertificates", "elasticloadbalancing:RemoveListenerCertificates"},
	"AWS::KinesisAnalyticsV2::ApplicationCloudWatchLoggingOption": {"kinesisanalytics:AddApplicationCloudWatchLoggingOption",
		"kinesisanalytics:DeleteApplicationCloudWatchLoggingOption", "kinesisanalytics:DescribeApplication", "kinesisanalytics:UpdateApplication"},
	"AWS::LakeFormation::DataLakeSettings": {"lakeformation:GetDataLakeSettings", "lakeformation:PutDataLakeSettings"},
	// Lake Formation checks the Glue resource with the caller's credentials (not in CloudTrail).
	// glue:GetTable is for TableResource grants, which bench did not exercise.
	"AWS::LakeFormation::Permissions": {"glue:GetDatabase", "glue:GetTable", "lakeformation:GrantPermissions", "lakeformation:RevokePermissions"},
	"AWS::LakeFormation::Resource": {"iam:GetRole", "iam:PassRole", "lakeformation:DeregisterResource", "lakeformation:RegisterResource",
		"lakeformation:UpdateResource"},
	"AWS::MediaLive::Channel": {"iam:PassRole", "medialive:CreateChannel", "medialive:CreateTags", "medialive:DeleteChannel",
		"medialive:DeleteTags", "medialive:DescribeChannel", "medialive:UpdateChannel"},
	"AWS::MediaLive::Input": {"iam:PassRole", "medialive:CreateInput", "medialive:CreateTags", "medialive:DeleteInput",
		"medialive:DeleteTags", "medialive:DescribeInput", "medialive:UpdateInput"},
	"AWS::MediaLive::InputSecurityGroup": {"medialive:CreateInputSecurityGroup", "medialive:CreateTags", "medialive:DeleteInputSecurityGroup", "medialive:DeleteTags"},
	"AWS::CloudFormation::WaitCondition": {},
	"AWS::EC2::ClientVpnAuthorizationRule": {"ec2:AuthorizeClientVpnIngress", "ec2:DescribeClientVpnAuthorizationRules",
		"ec2:RevokeClientVpnIngress"},
	"AWS::EC2::ClientVpnEndpoint":  {"ec2:CreateClientVpnEndpoint", "ec2:CreateTags", "ec2:DeleteClientVpnEndpoint", "ec2:DeleteTags", "ec2:DescribeClientVpnEndpoints"},
	"AWS::Route53::RecordSetGroup": {"route53:ChangeResourceRecordSets", "route53:GetChange", "route53:GetHostedZone"},
}

// passRole prefixes a token meaning "iam:PassRole, only to this service":
// "iam:PassRole>lambda.amazonaws.com". Documents turns tokens into one statement
// with an iam:PassedToService condition, and AllowedActions reads them back.
const passRole = "iam:PassRole>"

// passedToService maps types whose handlers pass a role to the service principal
// it is passed to, for --pass-role-condition (#7). An empty row means the type
// takes no role, so it needs no iam:PassRole. Types missing here keep a bare
// iam:PassRole, so the flag never breaks a deploy. bench/passrole/ checks each row.
var passedToService = map[string][]string{
	"AWS::ApiGateway::Method":          {"apigateway.amazonaws.com"},
	"AWS::ApiGateway::RestApi":         {},
	"AWS::ECS::Service":                {"ecs.amazonaws.com"},
	"AWS::ECS::TaskDefinition":         {"ecs-tasks.amazonaws.com"},
	"AWS::Events::Rule":                {"events.amazonaws.com"},
	"AWS::Lambda::Function":            {"lambda.amazonaws.com"},
	"AWS::S3::Bucket":                  {"s3.amazonaws.com"},
	"AWS::StepFunctions::StateMachine": {"states.amazonaws.com"},
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

// Statement is a statement on all resources.
type Statement struct {
	Sid       string                         `json:"Sid"`
	Effect    string                         `json:"Effect"`
	Action    []string                       `json:"Action"`
	Resource  string                         `json:"Resource"`
	Condition map[string]map[string][]string `json:"Condition,omitempty"`
}

// newDoc builds part i; the first part also carries the conditioned PassRole.
func newDoc(i int, actions, services []string) PolicyDocument {
	d := PolicyDocument{Version: "2012-10-17"}
	if len(actions) > 0 {
		d.Statement = append(d.Statement, Statement{
			Sid: fmt.Sprintf("CfnExecPolicy%d", i), Effect: "Allow", Action: actions, Resource: "*",
		})
	}
	if i == 1 && len(services) > 0 {
		d.Statement = append(d.Statement, Statement{
			Sid: "CfnExecPolicyPassRole", Effect: "Allow", Action: []string{"iam:PassRole"}, Resource: "*",
			Condition: map[string]map[string][]string{"StringEquals": {"iam:PassedToService": services}},
		})
	}
	return d
}

func size(d PolicyDocument) int {
	b, _ := json.Marshal(d)
	return len(b)
}

// Documents splits actions into as few policy documents as fit the size limit.
func Documents(actions []string) []PolicyDocument {
	sort.Strings(actions)
	var plain, services []string
	for _, a := range actions {
		if svc, ok := strings.CutPrefix(a, passRole); ok {
			services = append(services, svc)
		} else {
			plain = append(plain, a)
		}
	}
	var docs []PolicyDocument
	var cur []string
	for _, a := range plain {
		if len(cur) > 0 && size(newDoc(len(docs)+1, append(append([]string{}, cur...), a), services)) > MaxPolicySize {
			docs = append(docs, newDoc(len(docs)+1, cur, services))
			cur = nil
		}
		cur = append(cur, a)
	}
	if len(cur) > 0 || len(docs) == 0 && len(services) > 0 {
		docs = append(docs, newDoc(len(docs)+1, cur, services))
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
		var as []string
		switch a := s["Action"].(type) {
		case string:
			as = []string{a}
		case []any:
			for _, x := range a {
				if str, ok := x.(string); ok {
					as = append(as, str)
				}
			}
		}
		if services := passedTo(s); services != nil && len(as) == 1 && as[0] == "iam:PassRole" {
			for _, svc := range services {
				out = append(out, passRole+svc)
			}
			continue
		}
		out = append(out, as...)
	}
	return out, nil
}

// passedTo returns the services of the condition Documents writes, or nil.
func passedTo(s map[string]any) []string {
	cond, _ := s["Condition"].(map[string]any)
	eq, _ := cond["StringEquals"].(map[string]any)
	if len(cond) != 1 || len(eq) != 1 {
		return nil
	}
	var out []string
	switch v := eq["iam:PassedToService"].(type) {
	case string:
		out = []string{v}
	case []any:
		for _, x := range v {
			if str, ok := x.(string); ok {
				out = append(out, str)
			}
		}
	}
	return out
}
