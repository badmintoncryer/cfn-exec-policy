package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// fakeLastAccess reports, per policy ARN, when each service was last used
// (zero: never), after one in-progress read, two services per page.
type fakeLastAccess struct {
	created map[string]time.Time
	last    map[string]map[string]time.Time
	reads   map[string]int
}

func (f *fakeLastAccess) GetPolicy(_ context.Context, in *iam.GetPolicyInput, _ ...func(*iam.Options)) (*iam.GetPolicyOutput, error) {
	c := f.created[*in.PolicyArn]
	return &iam.GetPolicyOutput{Policy: &iamtypes.Policy{PolicyName: in.PolicyArn, CreateDate: &c}}, nil
}

func (f *fakeLastAccess) GenerateServiceLastAccessedDetails(_ context.Context, in *iam.GenerateServiceLastAccessedDetailsInput, _ ...func(*iam.Options)) (*iam.GenerateServiceLastAccessedDetailsOutput, error) {
	return &iam.GenerateServiceLastAccessedDetailsOutput{JobId: in.Arn}, nil
}

func (f *fakeLastAccess) GetServiceLastAccessedDetails(_ context.Context, in *iam.GetServiceLastAccessedDetailsInput, _ ...func(*iam.Options)) (*iam.GetServiceLastAccessedDetailsOutput, error) {
	f.reads[*in.JobId]++
	if f.reads[*in.JobId] == 1 {
		return &iam.GetServiceLastAccessedDetailsOutput{JobStatus: iamtypes.JobStatusTypeInProgress}, nil
	}
	var all []iamtypes.ServiceLastAccessed
	for _, ns := range sortedKeys(f.last[*in.JobId]) {
		s := iamtypes.ServiceLastAccessed{ServiceNamespace: aws.String(ns)}
		if t := f.last[*in.JobId][ns]; !t.IsZero() {
			s.LastAuthenticated = &t
		}
		all = append(all, s)
	}
	start := 0
	if in.Marker != nil {
		start = len(*in.Marker)
	}
	end := min(start+2, len(all))
	out := &iam.GetServiceLastAccessedDetailsOutput{JobStatus: iamtypes.JobStatusTypeCompleted, ServicesLastAccessed: all[start:end]}
	if end < len(all) {
		out.IsTruncated, out.Marker = true, aws.String(strings.Repeat("x", end))
	}
	return out, nil
}

// A service counts as used when any part of the policy used it within the window.
func TestUnusedServices(t *testing.T) {
	pollEvery = time.Millisecond
	now := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	old := now.AddDate(-1, 0, 0)
	f := &fakeLastAccess{
		created: map[string]time.Time{"p": old, "p-2": old},
		last: map[string]map[string]time.Time{
			"p":   {"s3": now.AddDate(0, 0, -1), "sns": now.AddDate(0, 0, -120), "appmesh": {}, "sqs": now.AddDate(0, 0, -100)},
			"p-2": {"sqs": now.AddDate(0, 0, -89), "kinesis": now.AddDate(0, 0, -91)},
		},
		reads: map[string]int{},
	}
	got, err := UnusedServices(context.Background(), f, []string{"p", "p-2"}, 90, now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "appmesh,kinesis,sns" {
		t.Errorf("unused = %v", got)
	}
	f.created["p-2"] = now.AddDate(0, 0, -30)
	if _, err := UnusedServices(context.Background(), f, []string{"p", "p-2"}, 90, now); err == nil || !strings.Contains(err.Error(), "less than 90 days") {
		t.Errorf("young policy: %v", err)
	}
	kept, dropped := DropServices([]string{"sns:CreateTopic", "sns:DeleteTopic", "s3:CreateBucket", "*"}, got)
	if strings.Join(kept, ",") != "s3:CreateBucket,*" || len(dropped) != 2 {
		t.Errorf("kept %v dropped %v", kept, dropped)
	}
}
