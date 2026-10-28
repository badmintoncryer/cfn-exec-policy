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
| `generate [inputs]` | Print the policy document(s) | no |
| `check [inputs]` | Exit 1 if the managed policy is missing actions your templates need — put it in CI before `cdk deploy`. It reads the policy by name; it does not verify the exec role has it attached | yes (read-only) |
| `diff --base <path> [inputs]` | Print the permissions a change adds or drops, compared with another `cdk.out` (or template), e.g. one synthesized from `main`. See [In pull requests](#in-pull-requests) | no |
| `apply [inputs]` | Create or update the managed policy, print the `cdk bootstrap` command | yes |
| `delete` | Delete the managed policy, its split parts and the `--strict` boundary. Stops if any of them is still attached. Without `--yes`, only prints what it would delete | yes |

Inputs are `cdk.out` directories (default: `./cdk.out`, including nested
stacks and Stages) or CloudFormation templates (JSON or YAML).

Flags: `--policy-name` (default `cfn-exec-policy`), `--refresh-schemas` (use the
latest CloudFormation schemas instead of the embedded table),
`--pass-role-condition` (see below), `--strict` and `--boundary` (see
[`--strict`](#--strict)), `apply --prune`, `delete --yes`, `diff --format`
and `diff --fail-on` (see below).

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
that it could not comment. It needs a Linux or macOS runner: the npm packages
have no Windows binary yet.

| Input | What it does |
|---|---|
| `path` | `cdk.out` or templates (space-separated) of the pull request. Default `cdk.out`. |
| `base` | `cdk.out` or template of the base branch. Required. |
| `fail-on` | Same as the flag. |
| `args` | More flags, e.g. `--pass-role-condition` or `--strict`. |
| `comment` | `false` to skip the comment. Default `true`. |
| `github-token` | Token used to comment. Default `github.token`. |

`@v1` runs the latest 1.x release; `@v1.6.0` pins that version.

## Permissions to run it

These are the permissions of whoever runs `check`, `apply` or `delete` (your
CLI or CI credentials), not of the execution role.

| Command | Actions |
|---|---|
| `check`, `apply` | `cloudformation:GetTemplate`, `cloudformation:ListStackResources` (deployed templates and nested stacks), `iam:GetPolicy`, `iam:GetPolicyVersion` |
| `apply` only | `cloudformation:DescribeStackResources` (finds the bootstrap stack for the printed command), `iam:CreatePolicy`, `iam:CreatePolicyVersion`, `iam:ListPolicyVersions`, `iam:DeletePolicyVersion` (deletes the oldest non-default version when the policy already has 5) |
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
notices a missing action or role, but not a widened `Resource` or `Condition`.

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

## Install

```sh
npx cfn-exec-policy …                                    # npm (also: npx cdk-exec-policy)
go install github.com/badmintoncryer/cfn-exec-policy@latest
```

Or download a binary from Releases.

## License

Apache-2.0
