# cfn-exec-policy

**For AWS CDK & CloudFormation.** Generate the IAM policy your CloudFormation execution role actually needs, so `cdk bootstrap` stops handing it `AdministratorAccess`.

```sh
npx cfn-exec-policy generate          # reads ./cdk.out, prints the policy
```

Status: pre-release. See [docs/design.md](docs/design.md).

License: Apache-2.0
