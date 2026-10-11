#!/bin/bash
# Evaluates the --strict policy and boundary with iam:SimulateCustomPolicy, next to the
# default policy as a control. Creates nothing. Usage: bench/strict/simulate.sh <dir>
# where <dir> holds boundary.json, strict.json and default.json (see README.md).
set -u
cd "$1"
A=$(aws sts get-caller-identity --query Account --output text)
B="arn:aws:iam::${A}:policy/cfnxp-strict-boundary"
OTHER="arn:aws:iam::${A}:policy/other"
R="arn:aws:iam::${A}:role"
CDK="${R}/cdk-hnb659fds-cfn-exec-role-${A}-ap-northeast-1"
fail=0
sim() { # label expect policy boundary(yes|no) action resource [key value]
  local args=(--policy-input-list "$(cat "$3")" --action-names "$5" --resource-arns "$6")
  [[ $4 == yes ]] && args+=(--permissions-boundary-policy-input-list "$(cat boundary.json)")
  [[ -n ${7:-} ]] && args+=(--context-entries "ContextKeyName=$7,ContextKeyValues=$8,ContextKeyType=string")
  local got r=OK
  got=$(aws iam simulate-custom-policy "${args[@]}" --query 'EvaluationResults[0].EvalDecision' --output text 2>&1)
  [[ $got == "$2" ]] || { r=MISMATCH; fail=1; }
  printf '%-8s %-58s expect=%-12s got=%s\n' $r "$1" "$2" "$got"
}
sim "CreateRole with the boundary" allowed strict.json yes iam:CreateRole "$R/cfnxp-strict-app-Role-x" iam:PermissionsBoundary "$B"
sim "CreateRole without a boundary" explicitDeny strict.json yes iam:CreateRole "$R/cfnxp-strict-app-Role-x"
sim "CreateRole with another boundary" explicitDeny strict.json yes iam:CreateRole "$R/cfnxp-strict-app-Role-x" iam:PermissionsBoundary "$OTHER"
sim "exec policy alone: CreateRole with another boundary" implicitDeny strict.json no iam:CreateRole "$R/x" iam:PermissionsBoundary "$OTHER"
sim "control: default, CreateRole without a boundary" allowed default.json no iam:CreateRole "$R/x"
sim "AttachRolePolicy to a bounded role" allowed strict.json yes iam:AttachRolePolicy "$R/cfnxp-strict-app-target" iam:PermissionsBoundary "$B"
sim "AttachRolePolicy to an unbounded role" explicitDeny strict.json yes iam:AttachRolePolicy "$R/someone-admin"
sim "PutRolePolicy to an unbounded role" explicitDeny strict.json yes iam:PutRolePolicy "$R/someone-admin"
sim "UpdateAssumeRolePolicy on an unbounded role" explicitDeny strict.json yes iam:UpdateAssumeRolePolicy "$R/someone-admin"
sim "control: default, AttachRolePolicy to an unbounded role" allowed default.json no iam:AttachRolePolicy "$R/someone-admin"
sim "AttachRolePolicy to a bounded cdk-* role" explicitDeny strict.json yes iam:AttachRolePolicy "$CDK" iam:PermissionsBoundary "$B"
sim "PutRolePolicy on a bounded cdk-* role" explicitDeny strict.json yes iam:PutRolePolicy "$CDK" iam:PermissionsBoundary "$B"
sim "DeleteRolePermissionsBoundary" explicitDeny strict.json yes iam:DeleteRolePermissionsBoundary "$R/cfnxp-strict-app-target" iam:PermissionsBoundary "$B"
sim "CreatePolicyVersion on the exec policy" explicitDeny strict.json yes iam:CreatePolicyVersion "arn:aws:iam::${A}:policy/cfnxp-strict"
sim "CreatePolicyVersion on the boundary" explicitDeny strict.json yes iam:CreatePolicyVersion "$B"
sim "control: CreatePolicyVersion on another policy" allowed strict.json yes iam:CreatePolicyVersion "arn:aws:iam::${A}:policy/app-policy"
sim "PassRole a stack role to Lambda" allowed strict.json yes iam:PassRole "$R/cfnxp-strict-app-Role-abc" iam:PassedToService lambda.amazonaws.com
sim "PassRole a stack role to EC2" implicitDeny strict.json yes iam:PassRole "$R/cfnxp-strict-app-Role-abc" iam:PassedToService ec2.amazonaws.com
sim "PassRole an outside role to Lambda" implicitDeny strict.json yes iam:PassRole "$R/cfnxp-strict-unbounded" iam:PassedToService lambda.amazonaws.com
sim "control: default, PassRole an outside role to Lambda" allowed default.json no iam:PassRole "$R/cfnxp-strict-unbounded" iam:PassedToService lambda.amazonaws.com
sim "CreateUser" explicitDeny strict.json yes iam:CreateUser "arn:aws:iam::${A}:user/x"
sim "lambda:CreateFunction" allowed strict.json yes lambda:CreateFunction "*"
exit $fail
