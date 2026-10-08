# --strict on bench (#8)

`app/` is the stack the policy is generated from (`apply --strict --policy-name cfnxp-strict bench/strict/app`).
The other templates are deployed through `cdk-cfnxp-strict-exec`, an execution role holding that policy
with `cfnxp-strict-boundary` as its permissions boundary. `run.sh` sets everything up, deploys, prints
PASS/FAIL per template and cleans up.

| Template | Expected |
|---|---|
| app/app.template.yaml | succeeds: bounded role, policy attached to it, Lambda passed the role |
| noboundary.yaml | fails at CreateRole under strict; succeeds through `cfnxp-default-exec` (the non-strict policy) |
| attach-bounded.yaml | succeeds: attaches a policy to a bounded role outside cdk-* |
| attach-cdk.yaml | fails: the same attach to a bounded `cdk-*` role |
| attach-unbounded.yaml | fails: attaching to a role without the boundary |
| pass-bounded.yaml | succeeds: the same, passing a bounded role named inside the stack prefix |
| pass-unbounded.yaml | fails: iam:PassRole of a role outside the stack's scope |
