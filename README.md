<p align="center">
  <img src="https://raw.githubusercontent.com/badmintoncryer/cfn-exec-policy/main/assets/logo.png" alt="cfn-exec-policy" width="104" height="104">
</p>

<h1 align="center">cfn-exec-policy</h1>

<p align="center">
  <strong>Least-privilege CloudFormation execution role for AWS CDK.</strong>
</p>

<p align="center">
  <a href="https://github.com/badmintoncryer/cfn-exec-policy/actions/workflows/ci.yml"><img src="https://github.com/badmintoncryer/cfn-exec-policy/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://www.npmjs.com/package/cfn-exec-policy"><img src="https://img.shields.io/npm/v/cfn-exec-policy.svg" alt="npm version"></a>
  <a href="https://www.npmjs.com/package/cfn-exec-policy"><img src="https://img.shields.io/npm/dt/cfn-exec-policy.svg" alt="npm total downloads"></a>
</p>

<p align="center"><b>English</b> | <a href="https://github.com/badmintoncryer/cfn-exec-policy/blob/main/README.ja.md">日本語</a></p>

**For AWS CDK & CloudFormation.** Generate the IAM policy your CloudFormation
execution role actually needs, so `cdk bootstrap` stops handing it
`AdministratorAccess`.

```sh
cdk synth
npx cfn-exec-policy apply        # creates/updates the managed policy "cfn-exec-policy"
# → prints: cdk bootstrap aws://123456789012/us-east-1 --cloudformation-execution-policies arn:aws:iam::123456789012:policy/cfn-exec-policy
```

Run the printed `cdk bootstrap` command once, and every deploy in that
environment runs with the generated policy instead of administrator access.
If your app uses a custom bootstrap qualifier, the command also has the
`--qualifier` and `--toolkit-stack-name` of that bootstrap (read from
`cdk.out`), so it doesn't rewrite the default `CDKToolkit` stack.

When your templates change, update the policy before you deploy:

```sh
cdk synth
npx cfn-exec-policy apply        # adds what the new templates need
cdk deploy
```

In CI, `npx cfn-exec-policy check` fails the job when the policy is missing
something, so the deploy fails fast instead of mid-rollout. Re-run
`cdk bootstrap` only when `apply` prints a different list of policy ARNs
(the policy grew past one document).

## Commands

| Command | What it does | Needs AWS credentials |
|---|---|---|
| `generate [inputs]` | Print the policy document(s). `--format terraform` prints `aws_iam_policy` resources (one per part) and `--format cdk` a TypeScript function that creates `ManagedPolicy`s, for teams that keep IAM in code instead of running `apply`. With `--explain`, print instead, per action, the resources that need it and why: schema handlers, a hand-written (measured) row, the `<service>:*` fallback, a custom resource, a nested stack, an SSM parameter type or a dynamic reference (`--format json` for other tools) | no |
| `check [inputs]` | Exit 1 if the managed policy is missing actions your templates need — put it in CI before `cdk deploy`. It reads the policy by name; it does not verify the exec role has it attached | yes (read-only) |
| `diff --base <path> [inputs]` | Print the permissions a change adds or drops, compared with another `cdk.out` (or template), e.g. one synthesized from `main`. See [In pull requests](#in-pull-requests) | no |
| `apply [inputs]` | Create or update the managed policy, print the `cdk bootstrap` command | yes |
| `delete` | Delete the managed policy, its split parts and the `--strict` boundary. Stops if any of them is still attached. Without `--yes`, only prints what it would delete | yes |
| `doctor <stack>` | Explain the permission errors of a stack's last deploy. See [When a deploy fails](#when-a-deploy-fails) | yes (read-only) |

Inputs are `cdk.out` directories (default: `./cdk.out`, including nested
stacks and Stages) or CloudFormation templates (JSON or YAML).

Flags: `--policy-name` (default `cfn-exec-policy`), `--refresh-schemas` (use the
latest CloudFormation schemas instead of the embedded table),
`--pass-role-condition` and `--region-condition` (see below), `--strict` and `--boundary` (see
[`--strict`](#--strict)), `--account` (see [Several accounts](#several-accounts)),
`--scope-resources` (see [Scoping resources](#scoping-resources)),
`apply --prune`, `apply --drop-unused` (see [Dropping unused services](#dropping-unused-services)),
`delete --yes`, `diff --format`
and `diff --fail-on` (see below), and `check --validate` / `check --no-new-access`
(see [IAM Access Analyzer](#iam-access-analyzer)).

### When a deploy fails

When a deploy fails because the execution role was denied something,
`doctor` reads the stack's last operation (and the nested stacks that failed
in it), and lists each denied resource with the action the error names:

```sh
npx cfn-exec-policy doctor MyStack --region eu-west-1
```

```
MyStack: 1 resource(s) failed on permissions in the last operation

  MyStack  Mesh (AWS::AppMesh::Mesh)  CREATE_FAILED
    User: arn:aws:sts::123456789012:assumed-role/cdk-hnb659fds-cfn-exec-role-.../AWSCloudFormation is not authorized to perform: appmesh:TagResource ...
    appmesh:TagResource: not in cfn-exec-policy

missing from cfn-exec-policy: appmesh:TagResource
  Run `cfn-exec-policy apply` from the cdk.out of this deploy; if it still leaves them out, the table is missing them.
```

An action the policy already allows is reported as denied by something
else (a permissions boundary, an SCP, a resource policy or a condition), and
an error that names no action (S3's `Access Denied`) points you to CloudTrail.
When the permission table would not grant the action to that resource type,
it also prints the line to paste into an issue.
`--format json` prints the same for other tools. It exits `3` when it finds a
permission error.

### Dropping unused services

`apply` never removes actions, because every app bootstrapped in the account
and region shares the policy. `apply --drop-unused 90` drops the actions of
services that no role the policy is attached to has called in the last 90
days, from IAM's [service last accessed data](https://docs.aws.amazon.com/IAM/latest/UserGuide/access_policies_last-accessed.html),
unless your templates need them. It stops if the policy is younger than the
window, since a service it has not used yet may simply not have been needed
yet. Pass the cdk.out of every app that shares the bootstrap, as for
`check --no-new-access`.

### Scoping resources

By default every statement has `Resource: "*"`. `--scope-resources`
(experimental, not yet checked on a real account) limits the SQS and SNS
actions to the stacks' own queues and topics: the names CloudFormation
generates from the stack name (`arn:*:sqs:*:*:MyStack-*`), names fixed in
the templates, and queue URLs or topic ARNs written in them. A service stays
on `*`, with a warning, when a resource that needs it points at a queue or
topic the templates don't resolve (an imported topic, a name from a
parameter) or is a type it does not scope yet. Inputs must be `cdk.out`
directories. Without `--prune`, `apply` keeps the queues and topics already in
the policy, and leaves a service on `*` while the policy still allows it there.
More types follow in [#32](https://github.com/badmintoncryer/cfn-exec-policy/issues/32).

### IAM Access Analyzer

- `check --validate` runs the policy the templates need (and, with `--strict`,
  the boundary `apply` would write) through Access Analyzer
  [`ValidatePolicy`](https://docs.aws.amazon.com/access-analyzer/latest/APIReference/API_ValidatePolicy.html)
  and prints every finding. Errors and security warnings fail the check.
- `check --no-new-access` asks Access Analyzer
  [`CheckNoNewAccess`](https://docs.aws.amazon.com/access-analyzer/latest/APIReference/API_CheckNoNewAccess.html)
  whether each deployed part of the policy grants anything beyond what the
  templates need, including a widened `Resource` or a dropped `Condition`
  that the plain `check` can't see. It fails when one does. In a bootstrap
  shared by several apps, the other apps' actions show up here too, so run it
  with the inputs of all of them.

Both are extra API calls, and Access Analyzer's custom policy checks are
[charged per call](https://aws.amazon.com/iam/access-analyzer/pricing/)
(`ValidatePolicy` is free).

### Several accounts

The policy lives in one account, but a `cdk.out` from CDK Pipelines often
deploys Stages to several. Pass `--account <id>` to use only the stacks that
deploy to that account (and the env-agnostic ones), and run once per account
with its credentials:

```sh
AWS_PROFILE=dev  npx cfn-exec-policy apply --account 111111111111
AWS_PROFILE=prod npx cfn-exec-policy apply --account 222222222222
```

`check` and `apply` stop if the credentials are for another account. Without
`--account`, the policy covers the stacks of every account, with a warning. A
nested stack template takes the account of the stacks in its directory, or is
kept for every account when they deploy to more than one.

## With a CloudFormation template

No CDK app is needed: pass the template files instead of `cdk.out`.
`generate`, `diff` and `generate --explain` need no AWS credentials:

```sh
npx cfn-exec-policy generate template.yaml > policy.json
npx cfn-exec-policy generate --explain template.yaml     # why each action is there
npx cfn-exec-policy diff --base base/template.yaml template.yaml
```

Without `cdk bootstrap`, the policy goes on a
[CloudFormation service role](https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/using-iam-servicerole.html)
that you pass to each deploy:

```sh
npx cfn-exec-policy generate template.yaml > policy.json
aws iam create-policy --policy-name cfn-exec-policy --policy-document file://policy.json
aws iam create-role --role-name cfn-exec-role --assume-role-policy-document \
  '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"cloudformation.amazonaws.com"},"Action":"sts:AssumeRole"}]}'
aws iam attach-role-policy --role-name cfn-exec-role \
  --policy-arn arn:aws:iam::123456789012:policy/cfn-exec-policy
aws cloudformation deploy --template-file template.yaml --stack-name my-stack \
  --role-arn arn:aws:iam::123456789012:role/cfn-exec-role
```

`apply template.yaml` creates or updates the managed policy in the same way
(keeping actions already in it unless `--prune`). It then prints a
`cdk bootstrap` command; take the policy ARNs from its
`--cloudformation-execution-policies` and attach them to your role instead.
A policy over IAM's size limit is split: `generate` then prints a JSON array
of documents (and a note on stderr), and each one needs its own policy on the
role. `check template.yaml` works in CI as with `cdk.out`, and
`doctor <stack>` reads any stack's last deploy.

Differences from `cdk.out`:

- A template file names no stack, so the deployed template is not read. When
  a change removes resources, CloudFormation needs permission to delete them:
  pass the deployed template as another input, e.g.
  `aws cloudformation get-template --stack-name my-stack --query TemplateBody --output text > deployed.yaml`
  (`--output json` for a stack created from a JSON template), then
  `npx cfn-exec-policy apply template.yaml deployed.yaml`.
- The region for `--region-condition` comes from `AWS_REGION` (or the CLI's
  region for `check` and `apply`).
- `--strict` and `--scope-resources` need `cdk.out`, since they rely on stack
  names.

## In pull requests

`diff` shows reviewers what a change does to the execution role, from two
`cdk.out` directories and no AWS credentials:

```sh
npx cfn-exec-policy diff --base cdk.out.main cdk.out
```

```
execution policy: +9 actions · -13 no longer needed

+ AWS::AppMesh::Mesh (new)
    ! grants every action of the service: the type has no measured permissions
    appmesh:*

+ AWS::SQS::Queue (new)
    sqs:CreateQueue, sqs:DeleteQueue, sqs:GetQueueAttributes, sqs:GetQueueUrl, sqs:ListQueueTags,
    sqs:SetQueueAttributes, sqs:TagQueue, sqs:UntagQueue

- AWS::SNS::Topic (removed)
    iam:GetRole, sns:CreateTopic, sns:DeleteTopic, ...

13 actions no longer needed; `apply` keeps them unless --prune
```

An action is added when nothing the base needs covers it, and each one is
listed under the resource types that need it. `!` marks the additions worth a
closer look: `<service>:*` for a type with no measured permissions,
`iam:PassRole` without an `iam:PassedToService` condition, and unknown types,
which get no permissions and fail to deploy. `--format md` prints a pull
request comment, and `--fail-on wildcard,passrole,unknown` (or `any`, for any
added action) exits `3` when the change adds one of them. `--pass-role-condition`
and `--strict` give the diff of the policy those flags write; with `--strict` it
also lists the new role patterns `iam:PassRole` reaches.

In GitHub Actions, synthesize both sides and let the action comment:

```yaml
name: exec-policy
on: pull_request
permissions: { contents: read, pull-requests: write }
jobs:
  exec-policy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
        with: { path: head }
      - uses: actions/checkout@v7
        with: { path: base, ref: "${{ github.base_ref }}" }
      - uses: actions/setup-node@v7
        with: { node-version: 22 }
      - run: npm ci && npx cdk synth -q
        working-directory: head
      - run: npm ci && npx cdk synth -q
        working-directory: base
      - uses: badmintoncryer/cfn-exec-policy@v1
        with: { base: base/cdk.out, path: head/cdk.out, fail-on: "wildcard,unknown" }
```

The action posts one comment and updates it on every push, writes the same
Markdown to the job summary, and fails the job when `fail-on` matches. On pull
requests from forks and Dependabot the token is read-only, so it only warns
that it could not comment.

| Input | What it does |
|---|---|
| `path` | `cdk.out` or templates (space-separated) of the pull request. Default `cdk.out`. |
| `base` | `cdk.out` or template of the base branch. Required. |
| `fail-on` | Same as the flag. |
| `args` | More flags, e.g. `--pass-role-condition` or `--strict`. |
| `comment` | `false` to skip the comment. Default `true`. |
| `github-token` | Token used to comment. Default `github.token`. |

`@v1` runs the latest 1.x release; `@v1.6.0` pins that version.

## At `cdk synth`

The npm package also has a CDK
[validation plugin](https://docs.aws.amazon.com/cdk/v2/guide/policy-validation-synthesis.html)
that runs `generate` at every `cdk synth` and reports, at the construct, what
the policy would have to guess:

```ts
import { ExecPolicy } from 'cfn-exec-policy';

const app = new App();
// ... your stacks
ExecPolicy.apply(app, { strict: true });
```

- Types without handler permissions (granted `<service>:*`) and unknown types
  are warnings; `failOnGuess: true` makes them errors.
- `strict: true` fails the synth on what `--strict` rejects: a role without
  the permissions boundary, or permissions granted to IAM users. `boundary`
  names your own boundary.
- `args`: more flags, e.g. `['--pass-role-condition']`.

`ExecPolicy.apply` needs an aws-cdk-lib with `Validations`; on older versions
pass `new ExecPolicyValidation({ ... })` in
`new App({ policyValidationBeta1: [...] })`. It needs no AWS credentials.

## Permissions to run it

These are the permissions of whoever runs `check`, `apply` or `delete` (your
CLI or CI credentials), not of the execution role.

| Command | Actions |
|---|---|
| `check`, `apply` | `cloudformation:GetTemplate`, `cloudformation:ListStackResources` (deployed templates and nested stacks), `iam:GetPolicy`, `iam:GetPolicyVersion` |
| `apply` only | `cloudformation:DescribeStackResources` (finds the bootstrap stack for the printed command), `iam:CreatePolicy`, `iam:CreatePolicyVersion`, `iam:ListPolicyVersions`, `iam:DeletePolicyVersion` (deletes the oldest non-default version when the policy already has 5) |
| `check --validate` | `access-analyzer:ValidatePolicy` |
| `check --no-new-access` | `access-analyzer:CheckNoNewAccess` |
| `apply --drop-unused` | `iam:GenerateServiceLastAccessedDetails`, `iam:GetServiceLastAccessedDetails` |
| `doctor` | `cloudformation:DescribeStackEvents`, `iam:GetPolicy`, `iam:GetPolicyVersion` |
| `delete` | `cloudformation:DescribeStackResources`, `iam:GetPolicy`, `iam:GetPolicyVersion`, `iam:ListEntitiesForPolicy`, `iam:ListPolicyVersions`, `iam:DeletePolicyVersion`, `iam:DeletePolicy` |

`sts:GetCallerIdentity` is also called, but it needs no permission.
`generate` and `--refresh-schemas` need no AWS permissions.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["cloudformation:GetTemplate", "cloudformation:ListStackResources", "cloudformation:DescribeStackResources"],
      "Resource": "arn:aws:cloudformation:*:<account>:stack/*"
    },
    {
      "Effect": "Allow",
      "Action": [
        "iam:GetPolicy", "iam:GetPolicyVersion",
        "iam:CreatePolicy", "iam:CreatePolicyVersion",
        "iam:ListPolicyVersions", "iam:DeletePolicyVersion"
      ],
      "Resource": "arn:aws:iam::<account>:policy/cfn-exec-policy*"
    }
  ]
}
```

Drop the last four IAM actions for a `check`-only CI role, and add
`iam:ListEntitiesForPolicy` and `iam:DeletePolicy` to run `delete`. If you change
`--policy-name`, change the resource to match. `apply --strict` writes
`<policy-name>-boundary` too, which the same resource covers.

`apply` lets its caller decide what the execution role can do, and therefore
what any deploy can do. Give it only to the people who already administer
your deploy setup. `cdk bootstrap` itself also needs to create IAM roles, so
the first `apply` and `cdk bootstrap` usually run with administrator
credentials.

## How it works

CloudFormation publishes a schema for every resource type, listing the IAM
actions each handler calls. For every resource type in your templates the
policy grants the union of the create, read, update and delete handler
permissions, so updates, replacements, rollbacks and deletes all work.

- `check` and `apply` also read the **currently deployed** templates
  (including nested stacks), so a resource you just removed still has its
  delete permissions.
- `apply` **never removes** actions already in the policy, because every app
  bootstrapped into the same account and region shares one execution role. Use
  `--prune` when you know the policy serves only this app. If that needs fewer
  split parts, the extra `cfn-exec-policy-N` policies are emptied (they stay
  attached until you re-run `cdk bootstrap` with the new list).
- Tag permissions from the schema's tagging section are added too, so changing
  or removing tags (CDK `Tags.of()`, stack tags) goes through.
- About 6% of resource types publish no handler permissions. Those measured on
  a real account (e.g. `AWS::CodeBuild::Project`, `AWS::LakeFormation::Resource`,
  `AWS::Route53::RecordSetGroup`) use a hand-written list; the rest (e.g.
  `AWS::AppMesh::Mesh`) get `<service>:*` plus `iam:PassRole` and a warning.
  Progress: [#5](https://github.com/badmintoncryer/cfn-exec-policy/issues/5).
- Custom resources get `lambda:InvokeFunction` / `sns:Publish`; dynamic
  references and SSM parameter types get the matching `ssm`, `secretsmanager`
  and `kms` reads.
- `iam:PassRole` is granted on `*` without an `iam:PassedToService` condition
  (whenever a schema lists it, and for the `<service>:*` fallback).
  `--pass-role-condition` limits it to the services your templates pass roles
  to, for the types checked on a real account (Lambda, Step Functions,
  EventBridge rules, ECS, S3 replication, API Gateway). If another type in the
  templates passes a role, `iam:PassRole` stays unconditioned and a warning
  names the type.
- The actions are allowed in every region. `--region-condition` adds an
  `aws:RequestedRegion` condition with the regions of your stacks (from
  `cdk.out`; env-agnostic stacks and template files use `AWS_REGION`, or the
  CLI's region for `check` and `apply`) plus `us-east-1`, where IAM,
  CloudFront, Route 53 and other global services take requests. The policy is
  shared by every region of the account, so `apply` keeps the regions already
  in it, and stops if the policy has no region condition yet (another region's
  bootstrap may use it) unless you pass `--prune`. `check` reports a missing
  region. Not yet checked on a real account: a resource that calls another
  region (other than `us-east-1`) fails to deploy.
- `Describe*` / `List*` actions of a service are merged into wildcards to stay
  under the size limit.
- Policies over IAM's 6,144-character limit are split into
  `cfn-exec-policy`, `cfn-exec-policy-2`, …

## What this is and isn't

The default mode shrinks what the execution role can do from *everything* to
*the services your templates use*. It is **not a security boundary**: a role
that can create IAM roles can still create an administrator role.

### `--strict`

`--strict` closes the ways to gain permissions through IAM. `apply --strict`
also writes a permissions boundary, `<policy-name>-boundary`, and the
`cdk bootstrap` command it prints adds `--custom-permissions-boundary`, which
puts the boundary on the execution role. The boundary allows everything
except:

- creating a role without this boundary, and attaching or adding policies to,
  or changing the trust policy of, a role without it
- removing a permissions boundary
- creating or editing the boundary or the `<policy-name>*` policies
- IAM users, their access keys and passwords, and group grants
- changing the CDK bootstrap roles (`cdk-*-role-<account>-*`), the execution
  role included

Every role your stacks create must carry the boundary, so set it for the whole
app in `cdk.json`:

```json
"context": { "@aws-cdk/core:permissionsBoundary": { "name": "cfn-exec-policy-boundary" } }
```

The boundary is on the execution role, which every app bootstrapped in that
account and region shares, so every one of those apps needs this setting.

`--strict` also changes the policy and the checks:

- Roles without a boundary, IAM users and group grants in your templates are
  an error before deploy.
- `iam:CreateRole` is allowed only with the boundary.
- `iam:PassRole` reaches only the roles CloudFormation names after your stacks
  (`<stack-name>-*`; a stack name over 25 characters is cut to its first 25),
  roles with a fixed `RoleName`, and role ARNs written in the templates. A
  role it cannot resolve, such as a `RoleName` from a parameter, gets a warning
  and cannot be passed. Implies `--pass-role-condition`. Without `--prune`,
  `apply` keeps the roles already in the policy, so the other apps sharing
  the execution role can still pass theirs, and `check` reports a missing one.
- A resource type with no known permissions is an error instead of
  `<service>:*`.
- `apply` stops if the policy already has an action such as `*` or `iam:*`,
  which would allow `iam:PassRole` and `iam:CreateRole` on every role.
- Inputs must be `cdk.out` directories, because the stack names are needed. A
  template file is an error.

`--boundary <name or ARN>` uses your own boundary instead; `apply` then does
not write one.

What `--strict` does not stop: the boundary allows everything outside IAM, so
a role your stack creates can still read or delete whatever its own policy
allows. A role with the boundary can also pass an existing role that has no
boundary to a service, because `iam:PassRole` has no condition key for
boundaries; only the execution role's own `iam:PassRole` is narrowed. `check`
notices a missing action or role, but not a widened `Resource` or `Condition`;
`check --no-new-access` does.

Roles deployed before you turn on `--strict` get the boundary on the next
deploy, even when that deploy also changes their inline or managed policies
(checked on a real account). See [docs/design.md](docs/design.md).

## Removing it

Point the execution role back at `AdministratorAccess` (or delete the
bootstrap stack) first, because IAM won't delete a policy that is still
attached. Then delete the policy:

```sh
cdk bootstrap aws://123456789012/us-east-1 --cloudformation-execution-policies arn:aws:iam::aws:policy/AdministratorAccess
npx cfn-exec-policy delete         # lists what it would delete
npx cfn-exec-policy delete --yes
```

`delete` also removes the split parts (`cfn-exec-policy-2`, …) and the old
versions that each `apply` leaves behind, which IAM requires before it deletes
a policy. If a part is still attached, it deletes nothing and prints the
`cdk bootstrap` command above.

After `apply --strict`, `delete` also removes `<policy-name>-boundary` (not a
boundary you passed with `--boundary`). IAM won't delete it while roles carry
it, so take it off them first, the execution role before the others, because
the boundary stops the execution role from removing boundaries:

1. The execution role: `cdk bootstrap` keeps the boundary when
   `--custom-permissions-boundary` is left out, even though it prints
   `Removing existing permissions boundary`. Clear the bootstrap stack's
   `InputPermissionsBoundary` parameter instead (CloudFormation console: Update
   stack, Use existing template), or delete the bootstrap stack.
2. Your stacks' roles: remove `@aws-cdk/core:permissionsBoundary` from
   `cdk.json` and redeploy (or destroy) each app.

`delete` lists the roles that still carry it.

## For AI agents

`cfn-exec-policy mcp` runs a [Model Context Protocol](https://modelcontextprotocol.io) server on
stdin/stdout, so an agent that writes CDK can see what its change asks of the
execution role:

| Tool | Runs | Needs AWS credentials |
|---|---|---|
| `exec_policy_explain` | `generate --explain --format json`: each action and the resources that need it | no |
| `exec_policy_generate` | `generate`: the policy document(s) | no |
| `exec_policy_diff` | `diff --format md --base <base>`: what a change adds or drops | no |
| `exec_policy_doctor` | `doctor <stack> --format json`: the permission errors of a failed deploy | yes (read-only) |

The template tools take `inputs` (default `./cdk.out` in the client's working
directory) and the `strict`, `pass_role_condition` and `scope_resources` switches.
For Claude Code:

```sh
claude mcp add exec-policy -- npx -y cfn-exec-policy mcp
```

or in any client's MCP configuration:

```json
{ "mcpServers": { "exec-policy": { "command": "npx", "args": ["-y", "cfn-exec-policy", "mcp"] } } }
```

## Install

```sh
npx cfn-exec-policy …                                    # npm (also: npx cdk-exec-policy)
go install github.com/badmintoncryer/cfn-exec-policy@latest
```

Or download a binary from Releases.

## License

Apache-2.0
