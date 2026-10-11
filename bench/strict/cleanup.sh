#!/bin/bash
# Removes the IAM roles and policies run.sh created.
A=$(aws sts get-caller-identity --query Account --output text)
# Roles still carrying the boundary (retained by a failed delete) block deleting it.
leftover=$(aws iam list-entities-for-policy --policy-arn arn:aws:iam::$A:policy/cfnxp-strict-boundary \
  --policy-usage-filter PermissionsBoundary --query 'PolicyRoles[].RoleName' --output text 2>/dev/null)
for r in $leftover cdk-cfnxp-strict-exec cfnxp-default-exec cfnxp-strict-app-target cdk-cfnxp-strict-target cfnxp-strict-unbounded; do
  for p in $(aws iam list-attached-role-policies --role-name $r --query 'AttachedPolicies[].PolicyArn' --output text 2>/dev/null); do
    aws iam detach-role-policy --role-name $r --policy-arn $p
  done
  for p in $(aws iam list-role-policies --role-name $r --query 'PolicyNames[]' --output text 2>/dev/null); do
    aws iam delete-role-policy --role-name $r --policy-name $p
  done
  aws iam delete-role --role-name $r 2>/dev/null && echo "deleted role $r"
done
# The boundary goes last: deleting it fails while a role still uses it.
for p in $(aws iam list-policies --scope Local --query "Policies[?(starts_with(PolicyName,'cfnxp-strict') || PolicyName=='cfnxp-default') && PolicyName!='cfnxp-strict-boundary'].Arn" --output text) \
  arn:aws:iam::$A:policy/cfnxp-strict-boundary; do
  for e in $(aws iam list-entities-for-policy --policy-arn $p --query 'PolicyRoles[].RoleName' --output text 2>/dev/null); do
    aws iam detach-role-policy --role-name $e --policy-arn $p
  done
  for v in $(aws iam list-policy-versions --policy-arn $p --query 'Versions[?!IsDefaultVersion].VersionId' --output text); do
    aws iam delete-policy-version --policy-arn $p --version-id $v
  done
  aws iam delete-policy --policy-arn $p && echo "deleted $p"
done
