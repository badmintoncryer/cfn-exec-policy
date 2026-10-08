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

`run.sh` (real deploys through CloudFormation) needs IAM role creation, which has to be
run by hand.
