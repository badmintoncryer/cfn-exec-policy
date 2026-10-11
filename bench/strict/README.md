# --strict on bench (#8)

`app/` is the stack the policy is generated from (`apply --strict --policy-name cfnxp-strict bench/strict/app`).
The other templates are deployed through `cdk-cfnxp-strict-exec`, an execution role holding that policy
with `cfnxp-strict-boundary` as its permissions boundary. `run.sh` sets everything up, deploys, prints
PASS/FAIL per template and deletes the stacks; `cleanup.sh` removes the IAM roles and policies.

| Template | Expected |
|---|---|
| app/app.template.yaml | succeeds: bounded role, policy attached to it, Lambda passed the role |
| noboundary.yaml | fails at CreateRole under strict; succeeds through `cfnxp-default-exec` (the non-strict policy) |
| attach-bounded.yaml | succeeds: attaches a policy to a bounded role outside cdk-* |
| attach-cdk.yaml | fails: the same attach to a bounded `cdk-*` role |
| attach-unbounded.yaml | fails: attaching to a role without the boundary |
| pass-bounded.yaml | succeeds: the same, passing a bounded role named inside the stack prefix |
| pass-unbounded.yaml | fails: iam:PassRole of a role outside the stack's scope |

## Without creating anything: `simulate.sh`

`simulate.sh <dir>` evaluates the policies with `iam:SimulateCustomPolicy`, with the
default policy as a control. `<dir>` holds `strict.json`
(`generate --strict --policy-name cfnxp-strict bench/strict/app`), `default.json`
(`generate bench/strict/app bench/strict/noboundary.yaml`) and `boundary.json` (the
document `apply --strict` writes as `cfnxp-strict-boundary`).

2026-10-09: 22/22 as expected. Under the boundary, creating a role without it, attaching
or putting policies on an unbounded role, changing its trust policy, changing a bounded
`cdk-*` role, removing a boundary, editing the exec policy or the boundary, and creating
a user are explicit denies; passing a role outside `cfnxp-strict-app-*`, or to a service
other than Lambda, is not allowed. The default policy allows the same role creation,
attach and PassRole. With `DenyRolesWithoutBoundary` and `DenyCdkRoles` removed from the
boundary, 7 cases mismatch, so the checks tell the two apart.

## Real deploys: `run.sh`

`run.sh` creates IAM roles, so run it by hand. 2026-10-09, ap-northeast-1, all as expected:

| Stack | Result |
|---|---|
| cfnxp-strict-app | PASS, and deleted through the strict role |
| cfnxp-strict-noboundary | FAIL: not authorized to perform `iam:CreateRole` |
| cfnxp-default-noboundary (control) | PASS: the default policy lets an admin role without a boundary through |
| cfnxp-strict-attach-bounded | PASS |
| cfnxp-strict-attach-cdk | FAIL: `iam:AttachRolePolicy` explicit deny in the boundary |
| cfnxp-strict-attach-unbounded | FAIL: `iam:AttachRolePolicy` explicit deny in the boundary |
| cfnxp-strict-pass-bounded | PASS |
| cfnxp-strict-pass-unbounded | FAIL: no identity-based policy allows `iam:PassRole` |

The simulator also showed explicit denies for `iam:CreatePolicyVersion` on the exec policy and
`iam:AttachRolePolicy` / `iam:PutRolePolicy` on the exec role itself.

The first runs found a bug the simulator missed: the boundary denied editing `policy/<policy-name>*`,
which also froze `cfnxp-strict-app-Policy-*`, so the app stack could not delete its own policy. The
boundary now names the policies exactly. `attach-cdk` still cannot be deleted cleanly: its rollback
detaches from a `cdk-*` role, which the boundary denies, so `run.sh` retains the policy and
`cleanup.sh` removes it.

## Roles that get the boundary later: `migrate.sh`

Each stack is created through a default (non-strict) exec role with a role that has no
boundary, updated through the strict exec role to add the boundary, then deleted through it.
`migrate.sh` sets up, runs and removes everything. 2026-10-09, ap-northeast-1:

| Variant | Phase 2 | Update | Delete |
|---|---|---|---|
| a | boundary only | PASS, boundary set | PASS |
| b | boundary + inline policy change | PASS, boundary set | PASS |
| c | boundary + another managed policy | PASS, boundary set | PASS |

CloudTrail shows `PutRolePermissionsBoundary` with `PutRolePolicy` (b) and `AttachRolePolicy` (c)
in the same second, all without errors. The boundary denies those calls on a role without it, so
CloudFormation must have set the boundary first.
