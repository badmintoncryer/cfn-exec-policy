#!/bin/bash
# Usage: bench/strict/run.sh (from the repo root, admin credentials). See README.md.
set -u
R=${AWS_REGION:-ap-northeast-1}; export AWS_REGION=$R
A=$(aws sts get-caller-identity --query Account --output text)
B=arn:aws:iam::$A:policy/cfnxp-strict-boundary
D=bench/strict
trust() { echo "{\"Statement\":[{\"Effect\":\"Allow\",\"Principal\":{\"Service\":\"$1\"},\"Action\":\"sts:AssumeRole\"}]}"; }

go run . apply --strict --policy-name cfnxp-strict $D/app || exit 1
aws iam create-role --role-name cdk-cfnxp-strict-exec --assume-role-policy-document "$(trust cloudformation.amazonaws.com)" --permissions-boundary $B >/dev/null
for p in $(aws iam list-policies --scope Local --query "Policies[?starts_with(PolicyName,'cfnxp-strict') && PolicyName!='cfnxp-strict-boundary'].Arn" --output text); do
  aws iam attach-role-policy --role-name cdk-cfnxp-strict-exec --policy-arn $p
done
aws iam create-role --role-name cfnxp-strict-app-target --assume-role-policy-document "$(trust lambda.amazonaws.com)" --permissions-boundary $B >/dev/null
aws iam create-role --role-name cdk-cfnxp-strict-target --assume-role-policy-document "$(trust lambda.amazonaws.com)" --permissions-boundary $B >/dev/null
aws iam create-role --role-name cfnxp-strict-unbounded --assume-role-policy-document "$(trust lambda.amazonaws.com)" >/dev/null
# Control: the default (non-strict) policy for the same templates.
go run . generate $D/app $D/noboundary.yaml > /tmp/cfnxp-default.json 2>/dev/null
aws iam create-policy --policy-name cfnxp-default --policy-document file:///tmp/cfnxp-default.json >/dev/null
aws iam create-role --role-name cfnxp-default-exec --assume-role-policy-document "$(trust cloudformation.amazonaws.com)" >/dev/null
aws iam attach-role-policy --role-name cfnxp-default-exec --policy-arn arn:aws:iam::$A:policy/cfnxp-default
sleep 20 # IAM propagation

deploy() { # stack template role
  aws cloudformation create-stack --stack-name $1 --template-body file://$2 --role-arn arn:aws:iam::$A:role/$3 --capabilities CAPABILITY_IAM >/dev/null
  if aws cloudformation wait stack-create-complete --stack-name $1 2>/dev/null; then echo "$1 PASS"
  else echo "$1 FAIL $(aws cloudformation describe-stack-events --stack-name $1 --query "StackEvents[?ResourceStatus=='CREATE_FAILED'].ResourceStatusReason" --output text | head -c 400 | tr '\n\t' '  ')"; fi
}
deploy cfnxp-strict-app $D/app/app.template.yaml cdk-cfnxp-strict-exec
for t in noboundary attach-bounded attach-cdk attach-unbounded pass-bounded pass-unbounded; do
  deploy cfnxp-strict-$t $D/$t.yaml cdk-cfnxp-strict-exec
done
deploy cfnxp-default-noboundary $D/noboundary.yaml cfnxp-default-exec

# What CloudFormation cannot attempt: editing the exec policy or the exec role itself.
aws iam simulate-principal-policy --policy-source-arn arn:aws:iam::$A:role/cdk-cfnxp-strict-exec \
  --action-names iam:CreatePolicyVersion --resource-arns arn:aws:iam::$A:policy/cfnxp-strict \
  --query 'EvaluationResults[].[EvalActionName,EvalDecision]' --output text
aws iam simulate-principal-policy --policy-source-arn arn:aws:iam::$A:role/cdk-cfnxp-strict-exec \
  --action-names iam:AttachRolePolicy iam:PutRolePolicy --resource-arns arn:aws:iam::$A:role/cdk-cfnxp-strict-exec \
  --query 'EvaluationResults[].[EvalActionName,EvalDecision]' --output text

# Cleanup
for s in cfnxp-strict-app cfnxp-strict-noboundary cfnxp-strict-attach-bounded cfnxp-strict-attach-cdk cfnxp-strict-attach-unbounded \
  cfnxp-strict-pass-bounded cfnxp-strict-pass-unbounded cfnxp-default-noboundary; do
  aws cloudformation delete-stack --stack-name $s --role-arn arn:aws:iam::$A:role/cfnxp-default-exec 2>/dev/null
done
for s in cfnxp-strict-app cfnxp-strict-noboundary cfnxp-strict-attach-bounded cfnxp-strict-attach-cdk cfnxp-strict-attach-unbounded \
  cfnxp-strict-pass-bounded cfnxp-strict-pass-unbounded cfnxp-default-noboundary; do
  aws cloudformation wait stack-delete-complete --stack-name $s 2>/dev/null || echo "$s delete FAIL"
done
echo DONE
