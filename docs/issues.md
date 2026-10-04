# Initial issues (to file once the GitHub repo exists)

1. **Schema table generator** — extract `type → union(create,read,update,delete).permissions` from `CloudformationSchema.zip`; derive each service namespace's IAM prefix from its handled types; embed as `table.json` (go:embed).
2. **Template loader** — CFN JSON/YAML (short-form tags tolerated); `cdk.out` manifest → stacks, nested assemblies (Stages), nested stacks via `aws:asset:path`.
3. **`generate`** — template(s) → policy document. Dedupe actions; split into multiple documents when over the 6,144-char managed-policy limit.
4. **Fallback for types without handlers** — `<service>:*` + warning (link to "add to the map" PR template).
5. **Hand-written map, first batch** — CodeBuild::Project, EMR::Cluster, Glue::Table, Route53::RecordSetGroup, IAM::AccessKey, CloudFormation::WaitCondition/WaitConditionHandle/CustomResource; each row verified on bench.
6. **Non-handler permissions** — dynamic references (`ssm:GetParameters`, `secretsmanager:GetSecretValue`, `kms:Decrypt`), `AWS::SSM::Parameter::Value<…>` parameters, asset bucket `s3:GetObject`, `iam:CreateServiceLinkedRole`.
7. **Bench check: custom resource ServiceToken** — does the exec role need `lambda:InvokeFunction` / `sns:Publish`?
8. **PassRole `iam:PassedToService`** — map resource types to the service principal they pass roles to.
9. **`check`** — union with deployed template (`GetTemplate`), diff against the attached policy, exit 1 if short.
10. **`apply`** — create/update managed policies, print `cdk bootstrap --cloudformation-execution-policies …`.
11. **`--refresh-schemas`** — fetch the latest bundle at runtime, cache it.
12. **Daily table refresh CI** — regenerate `table.json`, release when it changes.
13. **Release pipeline** — goreleaser: GitHub Releases, Homebrew tap, npm `optionalDependencies` packages, `cdk-exec-policy` alias.
14. **Bench E2E** — 5–10 CDK apps (Lambda+API, ECS, nested stacks, custom resources…) deploy/update/destroy with only the generated policy.
15. **`--strict` (v1.x)** — PassRole Resource scoping, boundary generation/enforcement, boundary-less role detection.
