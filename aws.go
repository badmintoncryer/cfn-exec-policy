package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// cfnAPI is the part of the CloudFormation client used here (a fake in tests).
type cfnAPI interface {
	GetTemplate(context.Context, *cloudformation.GetTemplateInput, ...func(*cloudformation.Options)) (*cloudformation.GetTemplateOutput, error)
	ListStackResources(context.Context, *cloudformation.ListStackResourcesInput, ...func(*cloudformation.Options)) (*cloudformation.ListStackResourcesOutput, error)
}

type awsClients struct {
	cfg     aws.Config
	cfn     cfnAPI
	iam     *iam.Client
	account string
	part    string
}

func newClients(ctx context.Context) (*awsClients, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}
	id, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, err
	}
	a, err := arn.Parse(aws.ToString(id.Arn))
	if err != nil {
		return nil, err
	}
	return &awsClients{cfg: cfg, cfn: cloudformation.NewFromConfig(cfg), iam: iam.NewFromConfig(cfg),
		account: aws.ToString(id.Account), part: a.Partition}, nil
}

// deployedTemplates fetches the processed templates of the stacks that already exist,
// and of their nested stacks, so resources being removed still get delete permissions.
func (c *awsClients) deployedTemplates(ctx context.Context, tpls []*Template) ([]*Template, error) {
	var out []*Template
	for _, t := range tpls {
		if t.StackName == "" {
			continue
		}
		ts, err := c.deployed(ctx, t.StackName)
		if err != nil {
			if strings.Contains(err.Error(), "does not exist") {
				continue
			}
			return nil, err
		}
		out = append(out, ts...)
	}
	return out, nil
}

// deployed returns the template of stack (a name or ARN) and, recursively, of its nested stacks.
func (c *awsClients) deployed(ctx context.Context, stack string) ([]*Template, error) {
	res, err := c.cfn.GetTemplate(ctx, &cloudformation.GetTemplateInput{
		StackName: aws.String(stack), TemplateStage: cfntypes.TemplateStageProcessed,
	})
	if err != nil {
		return nil, fmt.Errorf("get template %s: %w", stack, err)
	}
	t, err := ParseTemplate("deployed:"+stack, []byte(aws.ToString(res.TemplateBody)))
	if err != nil {
		return nil, err
	}
	out := []*Template{t}
	p := cloudformation.NewListStackResourcesPaginator(c.cfn, &cloudformation.ListStackResourcesInput{StackName: aws.String(stack)})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list resources %s: %w", stack, err)
		}
		for _, r := range page.StackResourceSummaries {
			if aws.ToString(r.ResourceType) != "AWS::CloudFormation::Stack" || aws.ToString(r.PhysicalResourceId) == "" {
				continue
			}
			nested, err := c.deployed(ctx, aws.ToString(r.PhysicalResourceId))
			if err != nil {
				return nil, err
			}
			out = append(out, nested...)
		}
	}
	return out, nil
}

func (c *awsClients) policyArn(name string) string {
	return fmt.Sprintf("arn:%s:iam::%s:policy/%s", c.part, c.account, name)
}

// policyNames returns name, name-2, name-3, … for n documents.
func policyNames(base string, n int) []string {
	out := []string{base}
	for i := 2; i <= n; i++ {
		out = append(out, fmt.Sprintf("%s-%d", base, i))
	}
	return out
}

// existingActions reads the Allow actions of base, base-2, … until one is missing.
// parts is how many of them exist (0 when base itself does not).
func (c *awsClients) existingActions(ctx context.Context, base string) (actions []string, parts int, err error) {
	for i := 1; ; i++ {
		name := policyNames(base, i)[i-1]
		p, err := c.iam.GetPolicy(ctx, &iam.GetPolicyInput{PolicyArn: aws.String(c.policyArn(name))})
		var nse *iamtypes.NoSuchEntityException
		if errors.As(err, &nse) {
			return actions, i - 1, nil
		}
		if err != nil {
			return nil, 0, err
		}
		v, err := c.iam.GetPolicyVersion(ctx, &iam.GetPolicyVersionInput{
			PolicyArn: p.Policy.Arn, VersionId: p.Policy.DefaultVersionId,
		})
		if err != nil {
			return nil, 0, err
		}
		doc, err := url.QueryUnescape(aws.ToString(v.PolicyVersion.Document))
		if err != nil {
			return nil, 0, err
		}
		as, err := AllowedActions([]byte(doc))
		if err != nil {
			return nil, 0, err
		}
		actions = append(actions, as...)
	}
}

// putPolicy creates the managed policy or adds a new default version, deleting the
// oldest non-default version when the 5-version limit is reached.
func (c *awsClients) putPolicy(ctx context.Context, name string, doc PolicyDocument) error {
	b, _ := json.Marshal(doc)
	policyArn := c.policyArn(name)
	_, err := c.iam.GetPolicy(ctx, &iam.GetPolicyInput{PolicyArn: aws.String(policyArn)})
	var nse *iamtypes.NoSuchEntityException
	if errors.As(err, &nse) {
		_, err = c.iam.CreatePolicy(ctx, &iam.CreatePolicyInput{
			PolicyName:     aws.String(name),
			Description:    aws.String("CloudFormation execution role policy generated by cfn-exec-policy"),
			PolicyDocument: aws.String(string(b)),
		})
		return err
	}
	if err != nil {
		return err
	}
	vs, err := c.iam.ListPolicyVersions(ctx, &iam.ListPolicyVersionsInput{PolicyArn: aws.String(policyArn)})
	if err != nil {
		return err
	}
	if len(vs.Versions) >= 5 {
		var old []iamtypes.PolicyVersion
		for _, v := range vs.Versions {
			if !v.IsDefaultVersion {
				old = append(old, v)
			}
		}
		sort.Slice(old, func(i, j int) bool { return old[i].CreateDate.Before(*old[j].CreateDate) })
		if _, err := c.iam.DeletePolicyVersion(ctx, &iam.DeletePolicyVersionInput{
			PolicyArn: aws.String(policyArn), VersionId: old[0].VersionId,
		}); err != nil {
			return err
		}
	}
	_, err = c.iam.CreatePolicyVersion(ctx, &iam.CreatePolicyVersionInput{
		PolicyArn: aws.String(policyArn), PolicyDocument: aws.String(string(b)), SetAsDefault: true,
	})
	return err
}
