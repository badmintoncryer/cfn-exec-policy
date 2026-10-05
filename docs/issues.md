# Open issues (to file once the GitHub repo exists)

Done in the initial implementation: schema table generator, template loader
(cdk.out, Stages, nested stacks, YAML), `generate` / `check` / `apply`,
`<service>:*` fallback, dynamic-reference and SSM-parameter permissions,
`--refresh-schemas`, daily table refresh workflow, goreleaser + npm packaging.

1. **Bench E2E** — 5–10 CDK apps (Lambda+API, ECS, nested stacks, custom resources, Stages) deploy/update/destroy with an exec role holding only the generated policy. Blocks v1.0.0.
2. **Verify `check` / `apply` against a real account** — GetTemplate union, policy versioning (5-version limit), multi-document split.
3. **Bench check: custom resource ServiceToken** — confirm the exec role needs `lambda:InvokeFunction` / `sns:Publish` (granted today on an unverified assumption).
4. **Hand-written map for types without handlers** — CodeBuild::Project, EMR::Cluster, Glue::Table, Route53::RecordSetGroup, IAM::AccessKey, CloudFormation::WaitCondition(Handle); each row verified on bench, replacing the `<service>:*` fallback.
5. **`--pass-role-condition` (opt-in, default off)** — add `iam:PassedToService` from a bench-verified type → service map (e.g. Lambda → `lambda.amazonaws.com`, RDS enhanced monitoring → `monitoring.rds.amazonaws.com`); unmapped types fall back to unconditioned PassRole with a warning. Implied by `--strict`.
6. **Deployed nested stacks** — `check`/`apply` don't fetch nested stacks of deployed parents, so a type removed only from a nested stack loses delete permissions.
7. **Release setup** — create `badmintoncryer/homebrew-tap`, add `HOMEBREW_TAP_GITHUB_TOKEN` and `NPM_TOKEN` secrets, run `goreleaser check`.
8. **`--strict` (v1.x)** — PassRole Resource scoping, boundary generation/enforcement, boundary-less role detection.
