package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"gopkg.in/yaml.v3"
)

// eventsAPI is the part of the CloudFormation client doctor uses (a fake in tests).
type eventsAPI interface {
	DescribeStackEvents(context.Context, *cloudformation.DescribeStackEventsInput, ...func(*cloudformation.Options)) (*cloudformation.DescribeStackEventsOutput, error)
}

// Denial is a resource whose last operation failed because the execution role
// was not allowed an action.
type Denial struct {
	Stack     string   `json:"stack"`
	LogicalID string   `json:"logicalId"`
	Type      string   `json:"type"`
	Status    string   `json:"status"`
	Actions   []string `json:"actions"` // empty when the message names none (e.g. S3's "Access Denied")
	Message   string   `json:"message"`
	// Granted lists the actions the deployed policy already allows: something
	// else denied them (a permissions boundary, an SCP, a resource policy or a condition).
	Granted []string `json:"granted,omitempty"`
}

// The ways a handler or service names an action it was denied. They are tried
// in order on each message; every match is kept.
var deniedPatterns = []*regexp.Regexp{
	// IAM: "... is not authorized to perform: ec2:CreateTags on resource: ..."
	regexp.MustCompile(`not authorized to perform:?\s+([a-z0-9-]+:[A-Za-z0-9*]+)`),
	// EC2 and other Query APIs via the handler: "API: ec2:CreateVpc You are not authorized to perform this operation."
	regexp.MustCompile(`API:\s+([a-z0-9-]+:[A-Za-z0-9*]+)\s+You are not authorized`),
	// "... because no identity-based policy allows the logs:CreateLogGroup action"
	regexp.MustCompile(`allows the ([a-z0-9-]+:[A-Za-z0-9*]+) action`),
	// "Access denied for operation 'kms:CreateGrant'" / "Access denied for operation 'AWS::Foo::Bar'."
	regexp.MustCompile(`[Aa]ccess denied for operation '([a-z0-9-]+:[A-Za-z0-9*]+)'`),
}

// deniedHint marks a message as a permission failure even when no action is named.
var deniedHint = regexp.MustCompile(`(?i)not authorized|access ?denied|AccessDenied|UnauthorizedOperation|AuthorizationError|is not authorized`)

// deniedActions returns the actions a failure message names, and whether the
// message is a permission failure at all.
func deniedActions(msg string) (actions []string, denied bool) {
	if !deniedHint.MatchString(msg) {
		return nil, false
	}
	for _, re := range deniedPatterns {
		for _, m := range re.FindAllStringSubmatch(msg, -1) {
			if !slices.Contains(actions, m[1]) {
				actions = append(actions, m[1])
			}
		}
	}
	sort.Strings(actions)
	return actions, true
}

// lastOperation returns the events of the stack's last operation, oldest first:
// the events since its last "User Initiated" event (or all of them).
func lastOperation(ctx context.Context, cfn eventsAPI, stack string) ([]cfntypes.StackEvent, error) {
	var events []cfntypes.StackEvent // newest first, as the API returns them
	var token *string
	for {
		out, err := cfn.DescribeStackEvents(ctx, &cloudformation.DescribeStackEventsInput{StackName: aws.String(stack), NextToken: token})
		if err != nil {
			return nil, fmt.Errorf("describe stack events %s: %w", stack, err)
		}
		for _, e := range out.StackEvents {
			events = append(events, e)
			if aws.ToString(e.LogicalResourceId) == aws.ToString(e.StackName) && aws.ToString(e.ResourceType) == "AWS::CloudFormation::Stack" &&
				strings.HasSuffix(string(e.ResourceStatus), "_IN_PROGRESS") && aws.ToString(e.ResourceStatusReason) == "User Initiated" {
				slices.Reverse(events)
				return events, nil
			}
		}
		if token = out.NextToken; token == nil {
			slices.Reverse(events)
			return events, nil
		}
	}
}

// Diagnose reads the last operation of stack (and of the nested stacks that
// failed in it) and returns the resources the execution role was denied.
func Diagnose(ctx context.Context, cfn eventsAPI, stack string) ([]Denial, error) {
	events, err := lastOperation(ctx, cfn, stack)
	if err != nil {
		return nil, err
	}
	var out []Denial
	seen := map[string]bool{}
	for _, e := range events {
		status := string(e.ResourceStatus)
		if !strings.HasSuffix(status, "_FAILED") {
			continue
		}
		id, typ, msg := aws.ToString(e.LogicalResourceId), aws.ToString(e.ResourceType), aws.ToString(e.ResourceStatusReason)
		if typ == "AWS::CloudFormation::Stack" && id != aws.ToString(e.StackName) && aws.ToString(e.PhysicalResourceId) != "" {
			nested, err := Diagnose(ctx, cfn, aws.ToString(e.PhysicalResourceId))
			if err == nil {
				out = append(out, nested...)
			}
			continue
		}
		actions, denied := deniedActions(msg)
		key := aws.ToString(e.StackName) + "/" + id + "/" + status
		if !denied || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, Denial{Stack: aws.ToString(e.StackName), LogicalID: id, Type: typ, Status: status, Actions: actions, Message: msg})
	}
	return out, nil
}

// MarkGranted fills in which denied actions the deployed policy already allows.
func MarkGranted(ds []Denial, have []string) {
	for i := range ds {
		for _, a := range ds[i].Actions {
			if Covered(a, have) {
				ds[i].Granted = append(ds[i].Granted, a)
			}
		}
	}
}

// WriteDoctor prints the denials and what to do about each.
func WriteDoctor(w io.Writer, stack string, ds []Denial, tbl *Table, policy string, havePolicy bool) {
	if len(ds) == 0 {
		fmt.Fprintf(w, "%s: no permission errors in the last operation\n", stack)
		return
	}
	fmt.Fprintf(w, "%s: %d resource(s) failed on permissions in the last operation\n\n", stack, len(ds))
	var missing, reports []string
	for _, d := range ds {
		fmt.Fprintf(w, "  %s  %s (%s)  %s\n", d.Stack, d.LogicalID, d.Type, d.Status)
		fmt.Fprintf(w, "    %s\n", oneLine(d.Message))
		switch {
		case len(d.Actions) == 0:
			fmt.Fprintln(w, "    ! the message names no action; check CloudTrail for the denied call")
		default:
			for _, a := range d.Actions {
				switch {
				case slices.Contains(d.Granted, a):
					fmt.Fprintf(w, "    %s: %s already allows it, so something else denied it (permissions boundary, SCP, resource policy or a condition)\n", a, policy)
				default:
					fmt.Fprintf(w, "    %s: not in %s\n", a, policy)
					missing = appendNew(missing, a)
					if tbl != nil && !tableHas(tbl, d.Type, a) {
						reports = appendNew(reports, d.Type+" needs "+a)
					}
				}
			}
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(w, "\nmissing from %s: %s\n", policy, strings.Join(missing, ", "))
		if havePolicy {
			fmt.Fprintln(w, "  Run `cfn-exec-policy apply` from the cdk.out of this deploy; if it still leaves them out, the table is missing them.")
		} else {
			fmt.Fprintf(w, "  %s was not found; run `cfn-exec-policy apply` first.\n", policy)
		}
	}
	if len(reports) > 0 {
		fmt.Fprintf(w, "\nthe permission table does not list these; please open an issue at https://github.com/badmintoncryer/cfn-exec-policy/issues with the lines below:\n  %s\n",
			strings.Join(reports, "\n  "))
	}
}

// WriteDoctorJSON prints the denials as JSON.
func WriteDoctorJSON(w io.Writer, ds []Denial) error {
	if ds == nil {
		ds = []Denial{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(ds)
}

// tableHas reports whether the table already grants a to resources of typ.
func tableHas(tbl *Table, typ, a string) bool {
	t := &Template{Path: "doctor"}
	t.Resources = map[string]struct {
		Type       string               `yaml:"Type"`
		Properties map[string]yaml.Node `yaml:"Properties"`
	}{"R": {Type: typ}}
	acts, _, _ := RequiredActions(tbl, []*Template{t}, false)
	return Covered(a, acts)
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
