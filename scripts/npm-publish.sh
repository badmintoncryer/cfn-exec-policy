#!/usr/bin/env bash
# Build per-platform binaries and publish them as npm packages:
#   cfn-exec-policy-<platform>-<arch>  (binary)
#   cfn-exec-policy                    (launcher, optionalDependencies on the above)
#   cdk-exec-policy                    (alias)
# Usage: scripts/npm-publish.sh <version> [--dry-run]
set -euo pipefail
VERSION=${1:?version}; shift
OUT=${NPM_OUT:-$(mktemp -d)}
TARGETS="darwin/arm64:darwin/arm64 darwin/amd64:darwin/x64 linux/amd64:linux/x64 linux/arm64:linux/arm64 windows/amd64:win32/x64"
OPT=""
for t in $TARGETS; do
  IFS=: read -r go node <<<"$t"
  goos=${go%/*} goarch=${go#*/} os=${node%/*} cpu=${node#*/}
  name="cfn-exec-policy-$os-$cpu"; dir="$OUT/$name"; exe=cfn-exec-policy; [ "$os" = win32 ] && exe=$exe.exe
  mkdir -p "$dir/bin"
  CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -ldflags "-s -w -X main.version=$VERSION" -o "$dir/bin/$exe" .
  cat >"$dir/package.json" <<JSON
{"name":"$name","version":"$VERSION","description":"cfn-exec-policy binary for $os-$cpu","license":"Apache-2.0",
 "repository":{"type":"git","url":"git+https://github.com/badmintoncryer/cfn-exec-policy.git"},"os":["$os"],"cpu":["$cpu"],"files":["bin"]}
JSON
  npm publish "$dir" --access public "$@"
  OPT="$OPT\"$name\":\"$VERSION\","
done
for p in cfn-exec-policy cdk-exec-policy; do
  cp -R "npm/$p" "$OUT/$p"; cp README.md LICENSE "$OUT/$p/"
done
deps="{${OPT%,}}"
cat >"$OUT/cfn-exec-policy/package.json" <<JSON
{"name":"cfn-exec-policy","version":"$VERSION","description":"Generate the IAM policy for your CloudFormation execution role from AWS CDK / CloudFormation templates",
 "license":"Apache-2.0","repository":{"type":"git","url":"git+https://github.com/badmintoncryer/cfn-exec-policy.git"},"keywords":["aws","aws-cdk","cdk","cloudformation","iam","least-privilege","bootstrap"],
 "bin":{"cfn-exec-policy":"bin/cfn-exec-policy.js"},"optionalDependencies":$deps}
JSON
npm publish "$OUT/cfn-exec-policy" --access public "$@"
cat >"$OUT/cdk-exec-policy/package.json" <<JSON
{"name":"cdk-exec-policy","version":"$VERSION","description":"Alias of cfn-exec-policy: least-privilege CloudFormation execution role policy for AWS CDK",
 "license":"Apache-2.0","repository":{"type":"git","url":"git+https://github.com/badmintoncryer/cfn-exec-policy.git"},"keywords":["aws","aws-cdk","cdk","cloudformation","iam","least-privilege","bootstrap"],
 "bin":{"cdk-exec-policy":"bin/cdk-exec-policy.js"},"dependencies":{"cfn-exec-policy":"$VERSION"}}
JSON
npm publish "$OUT/cdk-exec-policy" --access public "$@"
