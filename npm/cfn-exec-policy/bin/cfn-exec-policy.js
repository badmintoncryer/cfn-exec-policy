#!/usr/bin/env node
// Runs the platform binary installed via optionalDependencies.
const { spawnSync } = require('child_process');
const exe = process.platform === 'win32' ? 'cfn-exec-policy.exe' : 'cfn-exec-policy';
const pkg = `cfn-exec-policy-${process.platform}-${process.arch}`;
let bin;
try {
  bin = require.resolve(`${pkg}/bin/${exe}`);
} catch {
  console.error(`cfn-exec-policy: no prebuilt binary for ${process.platform}-${process.arch} (${pkg} is not installed).`);
  console.error('Install with optional dependencies enabled, or use `go install github.com/badmintoncryer/cfn-exec-policy@latest`.');
  process.exit(1);
}
const r = spawnSync(bin, process.argv.slice(2), { stdio: 'inherit' });
process.exit(r.status ?? 1);
