package main

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// --scope-resources narrows the actions of a few services from Resource "*" to
// the ARNs of the stacks' own resources: the names CloudFormation generates from
// the stack name, and names fixed in the templates. A service is narrowed only
// when every resource that needs its actions is a type below, and each of them
// names its target in a way the templates resolve; otherwise it stays on "*"
// with a warning. Every action of these services in the permission table takes
// the queue or topic as its resource (IAM Service Authorization Reference,
// servicereference.us-east-1.amazonaws.com, checked 2026-11-02).
// ponytail: not yet checked on a real account; start with SQS and SNS (issue #32).

// scopeRule says how a type names the resource its actions act on: by a name
// property (empty: generated), or by a property that points at it.
type scopeRule struct {
	nameProp string // the type is the resource; this property fixes its name
	target   string // the type acts on another resource named in this property (a value or a list)
	owner    string // the type a Ref/GetAtt in target must point at
}

var scopable = map[string]map[string]scopeRule{
	"sqs": {
		"AWS::SQS::Queue":             {nameProp: "QueueName"},
		"AWS::SQS::QueuePolicy":       {target: "Queues", owner: "AWS::SQS::Queue"},
		"AWS::SQS::QueueInlinePolicy": {target: "Queue", owner: "AWS::SQS::Queue"},
	},
	"sns": {
		"AWS::SNS::Topic":             {nameProp: "TopicName"},
		"AWS::SNS::TopicPolicy":       {target: "Topics", owner: "AWS::SNS::Topic"},
		"AWS::SNS::TopicInlinePolicy": {target: "TopicArn", owner: "AWS::SNS::Topic"},
		"AWS::SNS::Subscription":      {target: "TopicArn", owner: "AWS::SNS::Topic"},
	},
}

// resourceScopes is set by --scope-resources: service -> the resource ARNs its
// actions are allowed on. Documents read it.
var resourceScopes map[string][]string

var (
	sqsURL = regexp.MustCompile(`^https://sqs\.[a-z0-9-]+\.amazonaws\.com(?:\.cn)?/(\d{12})/([\w.-]+)$`)
	snsARN = regexp.MustCompile(`^arn:[\w-]+:sns:[a-z0-9-]+:\d{12}:[\w.-]+$`)
)

// ResourceScopes returns, for each scopable service the templates use, the ARN
// patterns its actions can be limited to, and warns about the services that stay on "*".
func ResourceScopes(tbl *Table, tpls []*Template) (scopes map[string][]string, warnings []string, err error) {
	scopes = map[string][]string{}
	for _, svc := range sortedKeys(scopable) {
		rules := scopable[svc]
		set, used, ok := map[string]bool{}, false, true
		arn := func(account, name string) string { return "arn:*:" + svc + ":*:" + account + ":" + name }
		for _, t := range tpls {
			prefix := ""
			switch {
			case t.StackName != "":
				prefix = t.StackName + "-*"
				if len(t.StackName) > stackPart {
					prefix = t.StackName[:stackPart] + "*"
				}
			case strings.HasSuffix(t.Path, ".nested.template.json") || strings.HasPrefix(t.Path, "deployed:"):
				// named after the parent stack, which the parent's pattern covers
			default:
				return nil, nil, fmt.Errorf("%s: --scope-resources needs stack names; pass the cdk.out directory instead of a template file", t.Path)
			}
			stackUsed := false
			for _, id := range sortedKeys(t.Resources) {
				r := t.Resources[id]
				if !needsService(tbl, r.Type, svc) {
					continue
				}
				used = true
				rule, known := rules[r.Type]
				if !known {
					warnings = append(warnings, fmt.Sprintf("%s: %s (%s) uses %s, which --scope-resources does not scope yet; %s actions stay on every resource", t.Path, id, r.Type, svc, svc))
					ok = false
					continue
				}
				if rule.nameProp != "" {
					n, fixed := r.Properties[rule.nameProp]
					switch name, lit := literal(n); {
					case !fixed:
						stackUsed = true
					case lit:
						set[arn("*", name)] = true
					default:
						warnings = append(warnings, fmt.Sprintf("%s: %s of %s is not a literal; %s actions stay on every resource", t.Path, rule.nameProp, id, svc))
						ok = false
					}
					continue
				}
				for _, v := range targets(r.Properties[rule.target]) {
					if to, isRef := refTo(v); isRef && t.Resources[to].Type == rule.owner {
						continue // a resource of this template, scoped by its own rule
					}
					s, lit := literal(v)
					switch m := sqsURL.FindStringSubmatch(s); {
					case lit && svc == "sqs" && m != nil:
						set[arn(m[1], m[2])] = true
					case lit && svc == "sns" && snsARN.MatchString(s):
						set[s] = true
					default:
						warnings = append(warnings, fmt.Sprintf("%s: %s of %s points at a %s resource outside this template; %s actions stay on every resource", t.Path, rule.target, id, svc, svc))
						ok = false
					}
				}
			}
			if stackUsed && prefix != "" {
				set[arn("*", prefix)] = true
			}
		}
		if used && ok {
			for a := range set {
				scopes[svc] = append(scopes[svc], a)
			}
			sort.Strings(scopes[svc])
		}
	}
	return scopes, warnings, nil
}

// needsService reports whether the table grants resources of typ actions of svc.
func needsService(tbl *Table, typ, svc string) bool {
	t := &Template{Path: "scope"}
	t.Resources = map[string]struct {
		Type       string               `yaml:"Type"`
		Properties map[string]yaml.Node `yaml:"Properties"`
	}{"R": {Type: typ}}
	acts, _, _ := RequiredActions(tbl, []*Template{t}, false)
	return slices.ContainsFunc(acts, func(a string) bool { return strings.HasPrefix(a, svc+":") })
}

// targets returns the items of a list property, or the property itself.
func targets(n yaml.Node) []yaml.Node {
	if n.Kind == yaml.SequenceNode {
		var out []yaml.Node
		for _, c := range n.Content {
			out = append(out, *c)
		}
		return out
	}
	if n.Kind == 0 {
		return nil
	}
	return []yaml.Node{n}
}

// refTo returns the logical ID a Ref or Fn::GetAtt (JSON or YAML short form) points at.
func refTo(n yaml.Node) (string, bool) {
	switch {
	case n.Tag == "!Ref" && n.Kind == yaml.ScalarNode:
		return n.Value, true
	case n.Tag == "!GetAtt" && n.Kind == yaml.ScalarNode:
		id, _, _ := strings.Cut(n.Value, ".")
		return id, true
	case n.Kind == yaml.MappingNode && len(n.Content) == 2:
		k, v := n.Content[0].Value, n.Content[1]
		switch {
		case k == "Ref" && v.Kind == yaml.ScalarNode:
			return v.Value, true
		case k == "Fn::GetAtt" && v.Kind == yaml.SequenceNode && len(v.Content) > 0:
			return v.Content[0].Value, true
		case k == "Fn::GetAtt" && v.Kind == yaml.ScalarNode:
			id, _, _ := strings.Cut(v.Value, ".")
			return id, true
		}
	}
	return "", false
}

// scopedActions splits off the actions of the services in resourceScopes.
func scopedActions(actions []string) (rest []string, scoped map[string][]string) {
	for _, a := range actions {
		svc, _, _ := strings.Cut(a, ":")
		if res := resourceScopes[svc]; len(res) > 0 {
			if scoped == nil {
				scoped = map[string][]string{}
			}
			scoped[svc] = append(scoped[svc], a)
		} else {
			rest = append(rest, a)
		}
	}
	return rest, scoped
}

// ScopedResources returns, per service, the resources the Allow statements of a
// policy document limit its actions to, and the services some statement allows on "*".
func ScopedResources(doc []byte) (scoped map[string][]string, everywhere []string, err error) {
	stmts, err := statements(doc)
	if err != nil {
		return nil, nil, err
	}
	scoped = map[string][]string{}
	for _, s := range stmts {
		if s["Effect"] != "Allow" {
			continue
		}
		res := stringList(s["Resource"])
		for _, a := range stringList(s["Action"]) {
			svc, _, _ := strings.Cut(a, ":")
			if _, ok := scopable[svc]; !ok {
				continue
			}
			if slices.Contains(res, "*") {
				everywhere = appendNew(everywhere, svc)
				continue
			}
			scoped[svc] = mergeRoles(scoped[svc], res)
		}
	}
	return scoped, everywhere, nil
}
