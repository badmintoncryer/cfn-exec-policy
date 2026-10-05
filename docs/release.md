# Releasing

Releases run in GitHub Actions on a `v*` tag (and from the daily `refresh-table`
workflow). They publish GitHub Release binaries with goreleaser and the npm
packages with trusted publishing (OIDC). There are no long-lived tokens or repo
secrets.

## One-time setup (npm)

Trusted publishing can only be configured for packages that already exist, so
the very first version is published from a laptop. Needs npm >= 11.10 and 2FA on
the npm account.

```sh
npm login
scripts/npm-publish.sh 0.0.1          # creates all 7 packages
pkgs="cfn-exec-policy cdk-exec-policy"
for p in darwin-arm64 darwin-x64 linux-x64 linux-arm64 win32-x64; do pkgs="$pkgs cfn-exec-policy-$p"; done
for p in $pkgs; do
  for f in release.yml refresh-table.yml; do   # workflow_call is checked against the caller
    npm trust github "$p" --repo badmintoncryer/cfn-exec-policy --file "$f" --allow-publish -y
  done
done
```

Then, on npmjs.com, set each package's publishing access to "Require two-factor
authentication and disallow tokens", so trusted publishing is the only way in.

## Cutting a release

```sh
git tag v0.1.0 && git push origin v0.1.0
```

A new platform (a new `cfn-exec-policy-<os>-<cpu>` package) needs the same
first-publish + `npm trust` step before its first release.
