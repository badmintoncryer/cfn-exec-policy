# cfn-exec-policy — design

Generate an IAM policy for the CloudFormation execution role from your templates,
so the role CDK bootstrap creates no longer needs `AdministratorAccess`.

## Problem

`cdk bootstrap` attaches `AdministratorAccess` to the CloudFormation execution
role unless you pass `--cloudformation-execution-policies`. Almost nobody does,
because writing that policy by hand means knowing every IAM action every
resource handler calls. The schemas CloudFormation publishes already contain
that list (`handlers.{create,read,update,delete}.permissions`); nothing turns it
into a policy and keeps it current.

Prior art: `cfnlp` (iann0036/aws-leastprivilege, last release 2020, needs AWS
credentials to fetch schemas), IAM Access Analyzer policy generation (needs a
real deploy + CloudTrail, misses `iam:PassRole`). aws/aws-cdk#27097 asks CDK to
require a scoped policy and is idle.

## Positioning

- The default mode **reduces surface area and must never break a deploy that
  Administrator would let through**. It is not a security boundary: a role that
  can `iam:CreateRole` + `iam:AttachRolePolicy` can still mint an admin role.
- `--strict` closes privilege escalation through IAM with a permissions boundary.
  The boundary allows everything else, so it does not limit what a bounded role
  can do to data or resources its own policy allows.
- Out of scope: protecting data inside the account (that is SCP / resource
  policies).

## Decisions

| # | Decision |
|---|---|
| Input | Any CloudFormation template(s). CDK is an adapter that reads `cdk.out` (manifest → stacks, recursing into nested stacks). |
| Output | Policy JSON (`generate`), CI gate (`check`), create/update a managed policy and print the `cdk bootstrap --cloudformation-execution-policies` command (`apply`). |
| Granularity | Per resource type, grant the **union of create/read/update/delete** handler permissions so replacement, rollback and delete all work. `Resource: "*"`. Schema lists are upper bounds (not per property); we emit them as-is, except that a service's `Describe*`/`List*` actions are merged into wildcards for size. |
| Tagging | Union the schema's `tagging.permissions` too (68 types list tag actions, e.g. `UntagResource`, outside their handlers). Bench 2026-10-06: tag add/change/remove on S3, Lambda, SES, Signer, IAM role passed even without them (handlers used `TagResource`/`UntagResource`), so the extra actions are a safety margin, kept per "never break a deploy Administrator would allow". |
| Removed types | Types removed from the template still need delete permissions on the next deploy. `check`/`apply` union the deployed template (`GetTemplate`) with the new one. |
| Missing handlers | ~124 of 1822 types have no `handlers` (CodeBuild::Project, EMR::Cluster, Route53::RecordSetGroup, CustomResource, WaitCondition, …); `Custom::*` and `AWS::Serverless::*` are not in the bundle. Fill from a hand-written map measured on bench (CloudTrail calls `invokedBy: cloudformation.amazonaws.com` during create/update/delete, plus `iam:PassRole` for role-ARN properties; see `bench/nohandler/`). Anything still unknown: default grants `<service>:*` with a warning; `--strict` errors. |
| Non-handler needs | Dynamic references (`ssm:GetParameters`, `secretsmanager:GetSecretValue`, `kms:Decrypt`), nested stack templates and Lambda code (`s3:GetObject` on the asset bucket), `iam:CreateServiceLinkedRole`. Custom resource `ServiceToken` invoke (`lambda:InvokeFunction`, verified on bench) and nested-stack change sets (`cloudformation:*ChangeSet`, not in the schema; found on bench). |
| PassRole | Default: bare `iam:PassRole` on `*` (never breaks a deploy). Opt-in `--pass-role-condition` (also implied by `--strict`): add `iam:PassedToService` limited to the services the template passes roles to, from a hand-written, bench-verified type → service map; a type missing from the map falls back to the unconditioned statement with a warning. `--strict` also narrows Resource to the roles CloudFormation names after each stack (`<stack-name>-*`; a stack name over 25 characters is cut to 25, measured on bench 2026-10-09 with logical IDs up to 200 characters), literal `RoleName`s and role ARNs written in the templates; unresolvable refs warn instead of widening. Template files without a stack name are an error. |
| Boundary (`--strict`) | `apply` writes `<policy-name>-boundary` and prints `cdk bootstrap --custom-permissions-boundary`, which puts it on the exec role (CDK's own `--example-permissions-boundary` leaves the `cdk-*` roles, the exec policy and existing roles open). Allow `*`; Deny, when the target role lacks this boundary (`iam:PermissionsBoundary`), `CreateRole`, `PutRolePermissionsBoundary`, `AttachRolePolicy`, `PutRolePolicy`, `UpdateAssumeRolePolicy`; Deny removing boundaries, editing the boundary and `<policy-name>*` policies, IAM users, access keys, login profiles and group grants, and changes to `cdk-*` roles. The exec policy also conditions `iam:CreateRole` on the boundary. `--boundary` takes your own (name or same-account ARN); then nothing is written. Roles without a boundary, IAM users and group grants in the input templates are an error before deploy, with the `@aws-cdk/core:permissionsBoundary` fix. Known gap: `iam:PassRole` has no boundary condition key, so a bounded role can still pass an existing unbounded role; only the exec role's own PassRole is scoped. Checked with the IAM policy simulator in `bench/strict/` (22 cases, with the default policy as a control); and with real deploys (`run.sh`, 8 stacks). Roles that get the boundary on a later update are not yet checked. |
| Schema data | Embed a pre-extracted `type → actions` table (small), regenerated by daily CI and released. `--refresh-schemas` fetches the latest bundle. Works offline, deterministic by default. |
| Language | Go, single binary. |
| Distribution | goreleaser → GitHub Releases, `go install`, npm (`optionalDependencies` per-platform binaries, esbuild style) via trusted publishing (OIDC, no tokens). `cdk-exec-policy` on npm is a thin alias of `cfn-exec-policy`. No Homebrew: a tap repo needs a cross-repo write credential; revisit via homebrew-core once there is demand. |
| License | Apache-2.0. |
| Verification | Unit tests (template → policy) and a bench E2E: 5–10 representative CDK apps deployed, updated and destroyed with an exec role holding only the generated policy. |

## v1 scope

`generate`, `check`, `apply`; default mode; `cdk.out` + nested stacks; embedded
table + `--refresh-schemas`; `<service>:*` fallback; npm + GitHub Releases.

`--pass-role-condition` (#7; rows checked in `bench/passrole/`, each with a
wrong-service control that must fail).

`--strict` (#8; boundary, PassRole scoping, boundary-less role detection,
unknown types as errors; checked in `bench/strict/`).

Later: growing the hand-written map, property-aware trimming of upper-bound lists.
