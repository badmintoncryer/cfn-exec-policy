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
| `apply [inputs]` | Create or update the managed policy, print the `cdk bootstrap` command | yes |

Inputs are `cdk.out` directories (default: `./cdk.out`, including nested
stacks and Stages) or CloudFormation templates (JSON or YAML).

Flags: `--policy-name` (default `cfn-exec-policy`), `--refresh-schemas` (use the
latest CloudFormation schemas instead of the embedded table),
`--pass-role-condition` (see below), `apply --prune`.

## Permissions to run it

These are the permissions of whoever runs `check` or `apply` (your CLI or CI
credentials), not of the execution role.

| Command | Actions |
|---|---|
| `check`, `apply` | `cloudformation:GetTemplate`, `cloudformation:ListStackResources` (deployed templates and nested stacks), `iam:GetPolicy`, `iam:GetPolicyVersion` |
| `apply` only | `iam:CreatePolicy`, `iam:CreatePolicyVersion`, `iam:ListPolicyVersions`, `iam:DeletePolicyVersion` (deletes the oldest non-default version when the policy already has 5) |

`sts:GetCallerIdentity` is also called, but it needs no permission.
`generate` and `--refresh-schemas` need no AWS permissions.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["cloudformation:GetTemplate", "cloudformation:ListStackResources"],
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

Drop the last four IAM actions for a `check`-only CI role. If you change
`--policy-name`, change the resource to match.

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
- About 7% of resource types publish no handler permissions. Those measured on
  a real account (e.g. `AWS::CodeBuild::Project`, `AWS::LakeFormation::Resource`,
  `AWS::Route53::RecordSetGroup`) use a hand-written list; the rest (e.g.
  `AWS::EMR::Cluster`) get `<service>:*` plus `iam:PassRole` and a warning.
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
that can create IAM roles can still create an administrator role. Closing that
(a permissions boundary on every role the stack creates) is planned as
`--strict`. See [docs/design.md](docs/design.md).

## Install

```sh
npx cfn-exec-policy …                                    # npm (also: npx cdk-exec-policy)
go install github.com/badmintoncryer/cfn-exec-policy@latest
```

Or download a binary from Releases.

## License

Apache-2.0
