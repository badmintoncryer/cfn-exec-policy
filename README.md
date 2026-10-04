# cfn-exec-policy

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

## Commands

| Command | What it does | Needs AWS credentials |
|---|---|---|
| `generate [inputs]` | Print the policy document(s) | no |
| `check [inputs]` | Exit 1 if the managed policy is missing actions your templates need — put it in CI before `cdk deploy`. It reads the policy by name; it does not verify the exec role has it attached | yes (read-only) |
| `apply [inputs]` | Create or update the managed policy, print the `cdk bootstrap` command | yes |

Inputs are `cdk.out` directories (default: `./cdk.out`, including nested
stacks and Stages) or CloudFormation templates (JSON or YAML).

Flags: `--policy-name` (default `cfn-exec-policy`), `--refresh-schemas` (use the
latest CloudFormation schemas instead of the embedded table), `apply --prune`.

## How it works

CloudFormation publishes a schema for every resource type, listing the IAM
actions each handler calls. For every resource type in your templates the
policy grants the union of the create, read, update and delete handler
permissions, so updates, replacements, rollbacks and deletes all work.

- `check` and `apply` also read the **currently deployed** templates, so a
  resource you just removed still has its delete permissions.
- `apply` **never removes** actions already in the policy, because every app
  bootstrapped into the same account and region shares one execution role. Use
  `--prune` when you know the policy serves only this app.
- About 7% of resource types publish no handler permissions (e.g.
  `AWS::CodeBuild::Project`, `AWS::EMR::Cluster`). For those it grants
  `<service>:*` plus `iam:PassRole` and prints a warning.
- Custom resources get `lambda:InvokeFunction` / `sns:Publish`; dynamic
  references and SSM parameter types get the matching `ssm`, `secretsmanager`
  and `kms` reads.
- `iam:PassRole` is granted on `*` without an `iam:PassedToService` condition in
  this release (whenever a schema lists it, and for the `<service>:*` fallback).
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
brew install badmintoncryer/tap/cfn-exec-policy          # Homebrew
go install github.com/badmintoncryer/cfn-exec-policy@latest
```

Or download a binary from Releases.

## License

Apache-2.0
