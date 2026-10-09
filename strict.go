package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Strict narrows the execution policy (--strict, #8): roles are created only with
// Boundary, and iam:PassRole reaches only Roles.
type Strict struct {
	Boundary string   // permissions boundary policy name, e.g. "cfn-exec-policy-boundary"
	Roles    []string // role ARN patterns iam:PassRole may pass
}

// stackPart is how much of a long stack name CloudFormation keeps in a generated
// role name: 64 characters minus the random suffix, split between stack name and
// logical ID. Measured 2026-10-09 with a 119-character stack name: 25 characters kept
// with 65- and 200-character logical IDs, 36 with a 5-character one; a 14-character
// stack name was kept whole next to a 200-character logical ID.
const stackPart = 25

// literal returns a plain YAML/JSON string value; intrinsics (!Ref, {"Fn::Sub"}) are not.
func literal(n yaml.Node) (string, bool) {
	if n.Kind == yaml.ScalarNode && n.Tag == "!!str" {
		return n.Value, true
	}
	return "", false
}

var roleARN = regexp.MustCompile(`:role/([\w+=,.@/-]+)(\$?)`)

// PassRoleScope returns the role ARN patterns the templates may pass: the roles
// CloudFormation names after each stack, roles with a literal RoleName, and role
// ARNs written in the templates. What it cannot resolve is warned about, not widened.
func PassRoleScope(tpls []*Template) (roles, warnings []string, err error) {
	set := map[string]bool{}
	add := func(path, name string) { set["arn:*:iam::*:role"+path+name] = true }
	for _, t := range tpls {
		prefix := "" // the start of the names CloudFormation generates for this stack's roles
		switch {
		case t.StackName != "":
			prefix = t.StackName + "-*"
			if len(t.StackName) > stackPart {
				prefix = t.StackName[:stackPart] + "*"
			}
			add("/", prefix)
		case strings.HasSuffix(t.Path, ".nested.template.json") || strings.HasPrefix(t.Path, "deployed:"):
			// A nested stack's name starts with its parent's, so the parent's pattern covers its roles.
		default:
			return nil, nil, fmt.Errorf("%s: --strict needs stack names to scope iam:PassRole; pass the cdk.out directory instead of a template file", t.Path)
		}
		for id, r := range t.Resources {
			if r.Type != "AWS::IAM::Role" {
				continue
			}
			path := "/"
			if n, ok := r.Properties["Path"]; ok {
				if path, ok = literal(n); !ok {
					warnings = append(warnings, fmt.Sprintf("%s: Path of %s is not a literal; iam:PassRole to it will be denied", t.Path, id))
					continue
				}
			}
			n, ok := r.Properties["RoleName"]
			switch {
			case !ok && path == "/":
			case !ok && prefix != "":
				add(path, prefix)
			case !ok:
				warnings = append(warnings, fmt.Sprintf("%s: %s has a Path in a nested stack; iam:PassRole to it will be denied", t.Path, id))
			default:
				if name, ok := literal(n); ok {
					add(path, name)
				} else {
					warnings = append(warnings, fmt.Sprintf("%s: RoleName of %s is not a literal; iam:PassRole to it will be denied", t.Path, id))
				}
			}
		}
		for _, m := range roleARN.FindAllStringSubmatch(string(t.Raw), -1) {
			if m[2] != "" {
				warnings = append(warnings, fmt.Sprintf("%s: role ARN %q is built from a parameter; iam:PassRole to it will be denied", t.Path, m[0]))
				continue
			}
			add("/", strings.TrimPrefix(m[1], "/"))
		}
	}
	for r := range set {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	return roles, warnings, nil
}

// StrictErrors lists the resources the boundary would reject: roles without a
// permissions boundary, and IAM users, access keys and group grants, which the
// boundary denies outright.
func StrictErrors(tpls []*Template, boundary string) []string {
	var errs []string
	missing := false
	for _, t := range tpls {
		ids := make([]string, 0, len(t.Resources))
		for id := range t.Resources {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			r := t.Resources[id]
			has := func(k string) bool { _, ok := r.Properties[k]; return ok }
			switch {
			case r.Type == "AWS::IAM::Role" && !has("PermissionsBoundary"):
				errs = append(errs, fmt.Sprintf("%s: %s has no PermissionsBoundary", t.Path, id))
				missing = true
			case r.Type == "AWS::IAM::User" || r.Type == "AWS::IAM::AccessKey" || r.Type == "AWS::IAM::UserToGroupAddition" ||
				r.Type == "AWS::IAM::UserPolicy" || r.Type == "AWS::IAM::GroupPolicy",
				r.Type == "AWS::IAM::Group" && (has("Policies") || has("ManagedPolicyArns")),
				(r.Type == "AWS::IAM::Policy" || r.Type == "AWS::IAM::ManagedPolicy") && (has("Users") || has("Groups")):
				errs = append(errs, fmt.Sprintf("%s: %s (%s) grants permissions to IAM users, which --strict denies; use roles", t.Path, id, r.Type))
			}
		}
	}
	if missing {
		errs = append(errs, fmt.Sprintf(`set the boundary on every role in cdk.json: "context": {"@aws-cdk/core:permissionsBoundary": {"name": %q}}`, boundary))
	}
	return errs
}

// BoundaryDocument is the permissions boundary --strict puts on the execution role
// and on every role the stacks create. It allows everything except escalating
// through IAM: creating or changing roles that lack this boundary, removing it,
// editing this tool's policies, IAM users and their credentials, and the CDK
// bootstrap roles.
func BoundaryDocument(partition, account, boundary, policyName string) PolicyDocument {
	arn := func(kind, name string) string {
		return fmt.Sprintf("arn:%s:iam::%s:%s/%s", partition, account, kind, name)
	}
	return PolicyDocument{Version: "2012-10-17", Statement: []Statement{
		{Sid: "AllowAll", Effect: "Allow", Action: []string{"*"}, Resource: "*"},
		{Sid: "DenyRolesWithoutBoundary", Effect: "Deny", Resource: "*",
			Action: []string{"iam:AttachRolePolicy", "iam:CreateRole", "iam:PutRolePermissionsBoundary",
				"iam:PutRolePolicy", "iam:UpdateAssumeRolePolicy"},
			Condition: map[string]map[string][]string{"StringNotEquals": {"iam:PermissionsBoundary": {arn("policy", boundary)}}}},
		{Sid: "DenyBoundaryRemoval", Effect: "Deny", Resource: "*",
			Action: []string{"iam:DeleteRolePermissionsBoundary", "iam:DeleteUserPermissionsBoundary"}},
		// policyName-? and -?? are the split parts (policyNames); a bare policyName* would
		// also match the policies of a stack whose name starts with policyName (bench, 2026-10-09).
		{Sid: "DenyPolicyEdits", Effect: "Deny", Resource: []string{arn("policy", boundary), arn("policy", policyName),
			arn("policy", policyName+"-?"), arn("policy", policyName+"-??")},
			Action: []string{"iam:CreatePolicyVersion", "iam:DeletePolicy", "iam:DeletePolicyVersion", "iam:SetDefaultPolicyVersion"}},
		{Sid: "DenyUsers", Effect: "Deny", Resource: "*",
			Action: []string{"iam:AddUserToGroup", "iam:AttachGroupPolicy", "iam:AttachUserPolicy", "iam:CreateAccessKey",
				"iam:CreateLoginProfile", "iam:CreateUser", "iam:PutGroupPolicy", "iam:PutUserPolicy", "iam:UpdateLoginProfile"}},
		{Sid: "DenyCdkRoles", Effect: "Deny", Resource: arn("role", "cdk-*"),
			Action: []string{"iam:Attach*", "iam:Delete*", "iam:Detach*", "iam:Put*", "iam:Tag*", "iam:Untag*", "iam:Update*"}},
	}}
}

// boundaryName accepts a policy name or a same-account policy ARN and returns the
// name CDK's --custom-permissions-boundary and permissionsBoundary context take.
func boundaryName(s, account string) (string, error) {
	if !strings.HasPrefix(s, "arn:") {
		return s, nil
	}
	parts := strings.SplitN(s, ":", 6)
	if len(parts) != 6 || parts[2] != "iam" || !strings.HasPrefix(parts[5], "policy/") {
		return "", fmt.Errorf("--boundary %s is not an IAM policy ARN", s)
	}
	if account != "" && parts[4] != account {
		return "", fmt.Errorf("--boundary %s is in account %s; a permissions boundary must be in %s", s, parts[4], account)
	}
	return strings.TrimPrefix(parts[5], "policy/"), nil
}
