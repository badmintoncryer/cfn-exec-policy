package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
)

// fakeEvents returns each stack's events newest first, two per page.
type fakeEvents map[string][]cfntypes.StackEvent

func (f fakeEvents) DescribeStackEvents(_ context.Context, in *cloudformation.DescribeStackEventsInput, _ ...func(*cloudformation.Options)) (*cloudformation.DescribeStackEventsOutput, error) {
	all := f[*in.StackName]
	start := 0
	if in.NextToken != nil {
		start = len(*in.NextToken)
	}
	end := min(start+2, len(all))
	out := &cloudformation.DescribeStackEventsOutput{StackEvents: all[start:end]}
	if end < len(all) {
		out.NextToken = aws.String(strings.Repeat("x", end))
	}
	return out, nil
}

func ev(stack, id, typ, status, reason string, physical ...string) cfntypes.StackEvent {
	e := cfntypes.StackEvent{StackName: aws.String(stack), LogicalResourceId: aws.String(id), ResourceType: aws.String(typ),
		ResourceStatus: cfntypes.ResourceStatus(status), ResourceStatusReason: aws.String(reason)}
	if len(physical) > 0 {
		e.PhysicalResourceId = aws.String(physical[0])
	}
	return e
}

func TestDeniedActions(t *testing.T) {
	for msg, want := range map[string]string{
		"Resource handler returned message: \"User: arn:aws:sts::123456789012:assumed-role/cdk-hnb659fds-cfn-exec-role-123456789012-us-east-1/AWSCloudFormation is not authorized to perform: logs:PutRetentionPolicy on resource: arn:aws:logs:us-east-1:123456789012:log-group:x because no identity-based policy allows the logs:PutRetentionPolicy action (Service: CloudWatchLogs, Status Code: 400)\"": "logs:PutRetentionPolicy",
		"API: ec2:CreateVpc You are not authorized to perform this operation.":   "ec2:CreateVpc",
		"Access denied for operation 'kms:CreateGrant'.":                         "kms:CreateGrant",
		"Access Denied (Service: S3, Status Code: 403, Request ID: X)":           "",
		"Resource of type 'AWS::SQS::Queue' with identifier 'q' already exists.": "-",
	} {
		got, denied := deniedActions(msg)
		switch {
		case want == "-" && denied:
			t.Errorf("%q: read as a denial", msg)
		case want != "-" && (!denied || strings.Join(got, ",") != want):
			t.Errorf("%q: %v %v, want %q", msg, got, denied, want)
		}
	}
}

// Only the last operation counts, nested stacks that failed are read too, and a
// denied action the policy already has is told apart from a missing one.
func TestDiagnose(t *testing.T) {
	f := fakeEvents{
		"App": { // newest first
			ev("App", "App", "AWS::CloudFormation::Stack", "UPDATE_ROLLBACK_COMPLETE", ""),
			ev("App", "Inner", "AWS::CloudFormation::Stack", "UPDATE_FAILED", "Embedded stack failed", "arn:inner"),
			ev("App", "Log", "AWS::Logs::LogGroup", "UPDATE_FAILED", "User: x is not authorized to perform: logs:PutRetentionPolicy on resource: y"),
			ev("App", "Queue", "AWS::SQS::Queue", "CREATE_FAILED", "Resource creation cancelled"),
			ev("App", "App", "AWS::CloudFormation::Stack", "UPDATE_IN_PROGRESS", "User Initiated"),
			ev("App", "Old", "AWS::SNS::Topic", "CREATE_FAILED", "User: x is not authorized to perform: sns:CreateTopic"),
			ev("App", "App", "AWS::CloudFormation::Stack", "CREATE_IN_PROGRESS", "User Initiated"),
		},
		"arn:inner": {
			ev("Inner-1", "Vpc", "AWS::EC2::VPC", "CREATE_FAILED", "API: ec2:CreateVpc You are not authorized to perform this operation."),
			ev("Inner-1", "Inner-1", "AWS::CloudFormation::Stack", "UPDATE_IN_PROGRESS", "User Initiated"),
		},
	}
	ds, err := Diagnose(context.Background(), f, "App")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range ds {
		got = append(got, d.LogicalID+":"+strings.Join(d.Actions, ","))
	}
	if strings.Join(got, " ") != "Log:logs:PutRetentionPolicy Vpc:ec2:CreateVpc" {
		t.Fatalf("denials: %v", got)
	}
	MarkGranted(ds, []string{"logs:*"})
	tbl, err := LoadTable(false)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	WriteDoctor(&b, "App", ds, tbl, "cfn-exec-policy", true)
	out := b.String()
	for _, want := range []string{"2 resource(s) failed", "logs:PutRetentionPolicy: cfn-exec-policy already allows it", "ec2:CreateVpc: not in cfn-exec-policy", "missing from cfn-exec-policy: ec2:CreateVpc"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "open an issue") { // the table has ec2:CreateVpc for AWS::EC2::VPC
		t.Errorf("asked for an issue for an action the table has:\n%s", out)
	}
	b.Reset()
	WriteDoctor(&b, "App", []Denial{{Stack: "App", LogicalID: "Mesh", Type: "AWS::Foo::Bar", Actions: []string{"foo:Baz"}}}, tbl, "p", true)
	if !strings.Contains(b.String(), "AWS::Foo::Bar needs foo:Baz") {
		t.Errorf("no issue line for an action the table lacks:\n%s", b.String())
	}
}
