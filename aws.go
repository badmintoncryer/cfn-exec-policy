package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
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
	DescribeStackResources(context.Context, *cloudformation.DescribeStackResourcesInput, ...func(*cloudformation.Options)) (*cloudformation.DescribeStackResourcesOutput, error)
}

// iamAPI is the part of the IAM client used here (a fake in tests).
type iamAPI interface {
	GetPolicy(context.Context, *iam.GetPolicyInput, ...func(*iam.Options)) (*iam.GetPolicyOutput, error)
	GetPolicyVersion(context.Context, *iam.GetPolicyVersionInput, ...func(*iam.Options)) (*iam.GetPolicyVersionOutput, error)
	CreatePolicy(context.Context, *iam.CreatePolicyInput, ...func(*iam.Options)) (*iam.CreatePolicyOutput, error)
	CreatePolicyVersion(context.Context, *iam.CreatePolicyVersionInput, ...func(*iam.Options)) (*iam.CreatePolicyVersionOutput, error)
	ListPolicyVersions(context.Context, *iam.ListPolicyVersionsInput, ...func(*iam.Options)) (*iam.ListPolicyVersionsOutput, error)
	DeletePolicyVersion(context.Context, *iam.DeletePolicyVersionInput, ...func(*iam.Options)) (*iam.DeletePolicyVersionOutput, error)
	DeletePolicy(context.Context, *iam.DeletePolicyInput, ...func(*iam.Options)) (*iam.DeletePolicyOutput, error)
	ListEntitiesForPolicy(context.Context, *iam.ListEntitiesForPolicyInput, ...func(*iam.Options)) (*iam.ListEntitiesForPolicyOutput, error)
}

type awsClients struct {
	cfg      aws.Config
	cfn      cfnAPI
	regional map[string]cfnAPI // other regions' clients, made by cfnIn
	iam      iamAPI
	account  string
	part     string
}

// cfnIn returns the CloudFormation client for region ("" for the configured one).
func (c *awsClients) cfnIn(region string) cfnAPI {
	if region == "" || region == c.cfg.Region {
		return c.cfn
	}
	if c.regional[region] == nil {
		if c.regional == nil {
			c.regional = map[string]cfnAPI{}
		}
		c.regional[region] = cloudformation.NewFromConfig(c.cfg, func(o *cloudformation.Options) { o.Region = region })
	}
	return c.regional[region]
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
		if t.Account != "" && t.Account != c.account {
			fmt.Fprintf(os.Stderr, "warning: %s deploys to account %s, not %s; its deployed template is not read (see --account)\n", t.StackName, t.Account, c.account)
			continue
		}
		ts, err := c.deployed(ctx, c.cfnIn(t.Region), t.StackName)
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
func (c *awsClients) deployed(ctx context.Context, cfn cfnAPI, stack string) ([]*Template, error) {
	res, err := cfn.GetTemplate(ctx, &cloudformation.GetTemplateInput{
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
	p := cloudformation.NewListStackResourcesPaginator(cfn, &cloudformation.ListStackResourcesInput{StackName: aws.String(stack)})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list resources %s: %w", stack, err)
		}
		for _, r := range page.StackResourceSummaries {
			if aws.ToString(r.ResourceType) != "AWS::CloudFormation::Stack" || aws.ToString(r.PhysicalResourceId) == "" {
				continue
			}
			nested, err := c.deployed(ctx, cfn, aws.ToString(r.PhysicalResourceId))
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

// existingActions reads the Allow actions of base, base-2, … until one is missing,
// and the roles their iam:PassRole statements are scoped to (--strict).
// parts is how many of them exist (0 when base itself does not).
func (c *awsClients) existingActions(ctx context.Context, base string) (actions, roles []string, parts int, err error) {
	for i := 1; ; i++ {
		name := policyNames(base, i)[i-1]
		p, err := c.iam.GetPolicy(ctx, &iam.GetPolicyInput{PolicyArn: aws.String(c.policyArn(name))})
		var nse *iamtypes.NoSuchEntityException
		if errors.As(err, &nse) {
			return actions, roles, i - 1, nil
		}
		if err != nil {
			return nil, nil, 0, err
		}
		v, err := c.iam.GetPolicyVersion(ctx, &iam.GetPolicyVersionInput{
			PolicyArn: p.Policy.Arn, VersionId: p.Policy.DefaultVersionId,
		})
		if err != nil {
			return nil, nil, 0, err
		}
		doc, err := url.QueryUnescape(aws.ToString(v.PolicyVersion.Document))
		if err != nil {
			return nil, nil, 0, err
		}
		as, err := AllowedActions([]byte(doc))
		if err != nil {
			return nil, nil, 0, err
		}
		rs, err := PassRoleResources([]byte(doc))
		if err != nil {
			return nil, nil, 0, err
		}
		actions = append(actions, as...)
		roles = append(roles, rs...)
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

// exists reports whether the managed policy name exists.
func (c *awsClients) exists(ctx context.Context, name string) (bool, error) {
	_, err := c.iam.GetPolicy(ctx, &iam.GetPolicyInput{PolicyArn: aws.String(c.policyArn(name))})
	var nse *iamtypes.NoSuchEntityException
	if errors.As(err, &nse) {
		return false, nil
	}
	return err == nil, err
}

// attachedTo returns the roles, users and groups the managed policy name is attached
// to or is the permissions boundary of.
func (c *awsClients) attachedTo(ctx context.Context, name string) ([]string, error) {
	var out []string
	p := iam.NewListEntitiesForPolicyPaginator(c.iam, &iam.ListEntitiesForPolicyInput{PolicyArn: aws.String(c.policyArn(name))})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range page.PolicyRoles {
			out = append(out, "role/"+aws.ToString(r.RoleName))
		}
		for _, u := range page.PolicyUsers {
			out = append(out, "user/"+aws.ToString(u.UserName))
		}
		for _, g := range page.PolicyGroups {
			out = append(out, "group/"+aws.ToString(g.GroupName))
		}
	}
	return out, nil
}

// deletePolicy deletes the managed policy name. IAM refuses to delete a policy that
// still has non-default versions, so those go first.
func (c *awsClients) deletePolicy(ctx context.Context, name string) error {
	policyArn := aws.String(c.policyArn(name))
	vs, err := c.iam.ListPolicyVersions(ctx, &iam.ListPolicyVersionsInput{PolicyArn: policyArn})
	if err != nil {
		return err
	}
	for _, v := range vs.Versions {
		if v.IsDefaultVersion {
			continue
		}
		if _, err := c.iam.DeletePolicyVersion(ctx, &iam.DeletePolicyVersionInput{PolicyArn: policyArn, VersionId: v.VersionId}); err != nil {
			return err
		}
	}
	_, err = c.iam.DeletePolicy(ctx, &iam.DeletePolicyInput{PolicyArn: policyArn})
	return err
}

const defaultQualifier = "hnb659fds"

// execRoles returns the names of the exec roles the cloud assembly deploys with, in
// the order first seen. Plain template files name none.
func execRoles(tpls []*Template, c *awsClients) []string {
	var out []string
	for _, t := range tpls {
		_, name, ok := strings.Cut(t.ExecRole, ":role/")
		if !ok {
			continue
		}
		// Environment-agnostic stacks leave these for the CLI to fill in.
		name = strings.NewReplacer("${AWS::AccountId}", c.account, "${AWS::Region}", c.cfg.Region).Replace(name)
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// qualifier returns the bootstrap qualifier in a default-named exec role
// (cdk-<qualifier>-cfn-exec-role-<account>-<region>), or "" for any other name.
func qualifier(role string) string {
	rest, ok := strings.CutPrefix(role, "cdk-")
	if !ok {
		return ""
	}
	q, _, ok := strings.Cut(rest, "-cfn-exec-role-")
	if !ok {
		return ""
	}
	return q
}

// roleRegion returns the region in a default-named exec role
// (cdk-<qualifier>-cfn-exec-role-<account>-<region>), or "" for any other name.
func roleRegion(role string) string {
	_, rest, ok := strings.Cut(role, "-cfn-exec-role-")
	if !ok || qualifier(role) == "" {
		return ""
	}
	_, region, _ := strings.Cut(rest, "-")
	return region
}

// bootstrapFlags returns the --qualifier and --toolkit-stack-name that make
// `cdk bootstrap` update the stack owning role, leaving out CDK's defaults, so a
// copied command doesn't rewrite the default CDKToolkit instead.
func (c *awsClients) bootstrapFlags(ctx context.Context, role string) string {
	var flags string
	q := qualifier(role)
	if q != "" && q != defaultQualifier {
		flags += " --qualifier " + q
	}
	res, err := c.cfnIn(roleRegion(role)).DescribeStackResources(ctx, &cloudformation.DescribeStackResourcesInput{PhysicalResourceId: aws.String(role)})
	if err != nil && !strings.Contains(err.Error(), "does not exist") {
		fmt.Fprintf(os.Stderr, "warning: can't find the bootstrap stack owning %s (%v); check --toolkit-stack-name before running this\n", role, err)
		return flags
	}
	if err != nil || len(res.StackResources) == 0 {
		// Not bootstrapped yet. Bootstrapping a new qualifier into the default
		// CDKToolkit would take that stack away from the qualifier it has now.
		if flags != "" {
			flags += " --toolkit-stack-name CDKToolkit-" + q
		}
		return flags
	}
	if s := aws.ToString(res.StackResources[0].StackName); s != "CDKToolkit" {
		flags += " --toolkit-stack-name " + s
	}
	return flags
}

// printBootstrap prints one `cdk bootstrap` command per exec role (one without
// environment flags when no role is known), each ending in opts.
func (c *awsClients) printBootstrap(ctx context.Context, roles []string, opts string) {
	if len(roles) == 0 {
		roles = []string{""}
	}
	for _, r := range roles {
		var flags string
		if r != "" {
			flags = c.bootstrapFlags(ctx, r)
		}
		region := roleRegion(r)
		if region == "" {
			region = c.cfg.Region
		}
		fmt.Printf("cdk bootstrap aws://%s/%s%s%s\n", c.account, region, flags, opts)
	}
}
