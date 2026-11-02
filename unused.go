package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// lastAccessAPI is the part of the IAM client --drop-unused uses (a fake in tests).
type lastAccessAPI interface {
	GetPolicy(context.Context, *iam.GetPolicyInput, ...func(*iam.Options)) (*iam.GetPolicyOutput, error)
	GenerateServiceLastAccessedDetails(context.Context, *iam.GenerateServiceLastAccessedDetailsInput, ...func(*iam.Options)) (*iam.GenerateServiceLastAccessedDetailsOutput, error)
	GetServiceLastAccessedDetails(context.Context, *iam.GetServiceLastAccessedDetailsInput, ...func(*iam.Options)) (*iam.GetServiceLastAccessedDetailsOutput, error)
}

// pollEvery is how long to wait between reads of a last-accessed job.
var pollEvery = 2 * time.Second

// UnusedServices returns the service namespaces that the policies allow but
// that no role they are attached to has called in the last days days, from
// IAM's service last accessed data. It refuses when a policy is younger than
// that, since a service it has not used yet may simply not have been needed yet.
func UnusedServices(ctx context.Context, api lastAccessAPI, policyArns []string, days int, now time.Time) ([]string, error) {
	since := now.AddDate(0, 0, -days)
	used, listed := map[string]bool{}, map[string]bool{}
	for _, arn := range policyArns {
		p, err := api.GetPolicy(ctx, &iam.GetPolicyInput{PolicyArn: aws.String(arn)})
		if err != nil {
			return nil, fmt.Errorf("get policy %s: %w", arn, err)
		}
		if c := p.Policy.CreateDate; c != nil && c.After(since) {
			return nil, fmt.Errorf("--drop-unused %d: %s was created %s, less than %d days ago, so a service it has not used may not have been needed yet",
				days, aws.ToString(p.Policy.PolicyName), c.Format("2006-01-02"), days)
		}
		job, err := api.GenerateServiceLastAccessedDetails(ctx, &iam.GenerateServiceLastAccessedDetailsInput{
			Arn: aws.String(arn), Granularity: iamtypes.AccessAdvisorUsageGranularityTypeServiceLevel,
		})
		if err != nil {
			return nil, fmt.Errorf("service last accessed details of %s: %w", arn, err)
		}
		var marker *string
		for deadline := time.Now().Add(5 * time.Minute); ; {
			out, err := api.GetServiceLastAccessedDetails(ctx, &iam.GetServiceLastAccessedDetailsInput{JobId: job.JobId, Marker: marker})
			if err != nil {
				return nil, fmt.Errorf("service last accessed details of %s: %w", arn, err)
			}
			switch out.JobStatus {
			case iamtypes.JobStatusTypeInProgress:
				if time.Now().After(deadline) {
					return nil, errors.New("service last accessed details: the job did not finish in 5 minutes")
				}
				time.Sleep(pollEvery)
				continue
			case iamtypes.JobStatusTypeFailed:
				msg := "unknown error"
				if out.Error != nil {
					msg = aws.ToString(out.Error.Message)
				}
				return nil, fmt.Errorf("service last accessed details of %s: %s", arn, msg)
			}
			for _, s := range out.ServicesLastAccessed {
				ns := aws.ToString(s.ServiceNamespace)
				listed[ns] = true
				if t := s.LastAuthenticated; t != nil && !t.Before(since) {
					used[ns] = true
				}
			}
			if !out.IsTruncated {
				break
			}
			marker = out.Marker
		}
	}
	var unused []string
	for ns := range listed {
		if !used[ns] {
			unused = append(unused, ns)
		}
	}
	slices.Sort(unused)
	return unused, nil
}

// DropServices returns the actions whose service is not in services.
func DropServices(actions, services []string) (kept, dropped []string) {
	for _, a := range actions {
		svc, _, _ := strings.Cut(a, ":")
		if slices.Contains(services, svc) {
			dropped = append(dropped, a)
		} else {
			kept = append(kept, a)
		}
	}
	return kept, dropped
}
