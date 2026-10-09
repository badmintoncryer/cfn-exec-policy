#!/bin/bash
# Usage: bench/strict/migrate.sh (from the repo root, admin credentials).
# Roles created without a boundary (default exec role), then updated to carry it through the
# strict exec role, then deleted through it. a: boundary only; b: + inline policy change;
# c: + another managed policy. Sets up, runs and removes everything; prints one line per step.
set -u
R=${AWS_REGION:-ap-northeast-1}; export AWS_REGION=$R
A=$(aws sts get-caller-identity --query Account --output text)
D=bench/strict/migrate
trust() { echo "{\"Statement\":[{\"Effect\":\"Allow\",\"Principal\":{\"Service\":\"$1\"},\"Action\":\"sts:AssumeRole\"}]}"; }
role() { echo arn:aws:iam::$A:role/$1; }
why() { aws cloudformation describe-stack-events --stack-name $1 --query "StackEvents[?contains(ResourceStatus,'FAILED')].ResourceStatusReason" --output text | head -c 400 | tr '\n\t' '  '; }

go run . apply --strict --policy-name cfnxp-mig $D >/dev/null || exit 1
aws iam create-role --role-name cdk-cfnxp-mig-exec --assume-role-policy-document "$(trust cloudformation.amazonaws.com)" \
  --permissions-boundary arn:aws:iam::$A:policy/cfnxp-mig-boundary >/dev/null
aws iam attach-role-policy --role-name cdk-cfnxp-mig-exec --policy-arn arn:aws:iam::$A:policy/cfnxp-mig
f=$(mktemp); go run . generate $D > $f 2>/dev/null
aws iam create-policy --policy-name cfnxp-mig-default --policy-document file://$f >/dev/null
aws iam create-role --role-name cfnxp-mig-default-exec --assume-role-policy-document "$(trust cloudformation.amazonaws.com)" >/dev/null
aws iam attach-role-policy --role-name cfnxp-mig-default-exec --policy-arn arn:aws:iam::$A:policy/cfnxp-mig-default
sleep 20 # IAM propagation

for v in a b c; do
  s=cfnxp-mig-$v
  aws cloudformation create-stack --stack-name $s --template-body file://$D/$v.yaml --role-arn $(role cfnxp-mig-default-exec) \
    --capabilities CAPABILITY_IAM --parameters ParameterKey=Phase,ParameterValue=1 >/dev/null
  aws cloudformation wait stack-create-complete --stack-name $s && echo "$v create (default role) PASS" || { echo "$v create FAIL $(why $s)"; continue; }
  aws cloudformation update-stack --stack-name $s --template-body file://$D/$v.yaml --role-arn $(role cdk-cfnxp-mig-exec) \
    --capabilities CAPABILITY_IAM --parameters ParameterKey=Phase,ParameterValue=2 >/dev/null
  if aws cloudformation wait stack-update-complete --stack-name $s 2>/dev/null; then
    rn=$(aws cloudformation describe-stack-resource --stack-name $s --logical-resource-id Role --query StackResourceDetail.PhysicalResourceId --output text)
    echo "$v update (strict role) PASS; boundary now: $(aws iam get-role --role-name $rn --query Role.PermissionsBoundary.PermissionsBoundaryArn --output text)"
  else
    echo "$v update (strict role) FAIL $(why $s)"
    st=$(aws cloudformation describe-stacks --stack-name $s --query 'Stacks[0].StackStatus' --output text)
    echo "$v after update: $st"
    if [[ $st == UPDATE_ROLLBACK_FAILED ]]; then
      aws cloudformation continue-update-rollback --stack-name $s --resources-to-skip Role
      aws cloudformation wait stack-rollback-complete --stack-name $s 2>/dev/null
    fi
  fi
done

for v in a b c; do
  s=cfnxp-mig-$v
  aws cloudformation delete-stack --stack-name $s
  if aws cloudformation wait stack-delete-complete --stack-name $s 2>/dev/null; then echo "$v delete PASS"
  else
    echo "$v delete FAIL $(why $s)"
    aws cloudformation delete-stack --stack-name $s --retain-resources Role
    aws cloudformation wait stack-delete-complete --stack-name $s
  fi
done

# Cleanup: retained stack roles first (they may carry the boundary), then the exec roles, then policies.
for r in $(aws iam list-roles --query "Roles[?starts_with(RoleName,'cfnxp-mig-')].RoleName" --output text) cdk-cfnxp-mig-exec; do
  for p in $(aws iam list-attached-role-policies --role-name $r --query 'AttachedPolicies[].PolicyArn' --output text); do
    aws iam detach-role-policy --role-name $r --policy-arn $p
  done
  for p in $(aws iam list-role-policies --role-name $r --query 'PolicyNames[]' --output text); do
    aws iam delete-role-policy --role-name $r --policy-name $p
  done
  aws iam delete-role --role-name $r && echo "cleanup: deleted role $r"
done
for p in cfnxp-mig cfnxp-mig-default cfnxp-mig-boundary; do
  aws iam delete-policy --policy-arn arn:aws:iam::$A:policy/$p && echo "cleanup: deleted $p"
done
echo DONE
