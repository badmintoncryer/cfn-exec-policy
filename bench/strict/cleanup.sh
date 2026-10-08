#!/bin/bash
# Removes the IAM roles and policies run.sh created.
for r in cdk-cfnxp-strict-exec cfnxp-default-exec cfnxp-strict-app-target cdk-cfnxp-strict-target cfnxp-strict-unbounded; do
  for p in $(aws iam list-attached-role-policies --role-name $r --query 'AttachedPolicies[].PolicyArn' --output text 2>/dev/null); do
    aws iam detach-role-policy --role-name $r --policy-arn $p
  done
  aws iam delete-role --role-name $r 2>/dev/null && echo "deleted role $r"
done
for p in $(aws iam list-policies --scope Local --query "Policies[?starts_with(PolicyName,'cfnxp-strict') || PolicyName=='cfnxp-default'].Arn" --output text); do
  for v in $(aws iam list-policy-versions --policy-arn $p --query 'Versions[?!IsDefaultVersion].VersionId' --output text); do
    aws iam delete-policy-version --policy-arn $p --version-id $v
  done
  aws iam delete-policy --policy-arn $p && echo "deleted $p"
done
