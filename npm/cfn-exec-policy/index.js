// The cfn-exec-policy binary, and a CDK policy validation plugin that reports,
// at `cdk synth`, what the execution role policy would have to guess.
const { spawnSync } = require('child_process');
const fs = require('fs');
const path = require('path');

/** The path of the platform binary installed via optionalDependencies. */
function binaryPath() {
  if (process.env.CFN_EXEC_POLICY_BINARY) return process.env.CFN_EXEC_POLICY_BINARY;
  const exe = process.platform === 'win32' ? 'cfn-exec-policy.exe' : 'cfn-exec-policy';
  const pkg = `cfn-exec-policy-${process.platform}-${process.arch}`;
  try {
    return require.resolve(`${pkg}/bin/${exe}`);
  } catch {
    throw new Error(`cfn-exec-policy: no prebuilt binary for ${process.platform}-${process.arch} (${pkg} is not installed). ` +
      'Install with optional dependencies enabled, or use `go install github.com/badmintoncryer/cfn-exec-policy@latest` and set CFN_EXEC_POLICY_BINARY.');
  }
}

// assemblyRoot is the top cloud assembly directory holding a template: CDK
// has written the manifests by the time plugins run.
function assemblyRoot(template) {
  let dir = path.dirname(path.resolve(template));
  if (!fs.existsSync(path.join(dir, 'manifest.json'))) return undefined;
  while (fs.existsSync(path.join(path.dirname(dir), 'manifest.json')) && path.dirname(dir) !== dir) {
    dir = path.dirname(dir);
  }
  return dir;
}

/** Each resource of the templates, by type: [{ logicalId, templatePath }]. */
function resourcesByType(paths) {
  const out = new Map();
  for (const p of paths) {
    let t;
    try {
      t = JSON.parse(fs.readFileSync(p, 'utf8'));
    } catch {
      continue;
    }
    for (const [id, r] of Object.entries(t.Resources || {})) {
      if (!out.has(r.Type)) out.set(r.Type, []);
      out.get(r.Type).push({ resourceLogicalId: id, templatePath: p, locations: [] });
    }
  }
  return out;
}

/**
 * A policy validation plugin: at `cdk synth`, runs `cfn-exec-policy generate`
 * on the app and reports the resource types it has to guess permissions for
 * (`<service>:*`) or doesn't know, as warnings. With `strict`, roles without
 * the permissions boundary and IAM user grants fail the synth.
 *
 * Options:
 * - strict: check what `--strict` requires (errors)
 * - boundary: with strict, your own boundary name (default cfn-exec-policy-boundary)
 * - failOnGuess: make the guessed and unknown types errors, not warnings
 * - args: more cfn-exec-policy flags, e.g. ['--pass-role-condition']
 */
class ExecPolicyValidation {
  constructor(options = {}) {
    this.name = 'cfn-exec-policy';
    this.options = options;
  }

  validate(context) {
    const o = this.options;
    const paths = context.stackTemplates ? context.stackTemplates.map((s) => s.templatePath) : context.templatePaths;
    const root = paths.length > 0 ? assemblyRoot(paths[0]) : undefined;
    // The whole assembly when there is one: nested stacks and Stages included,
    // and --strict needs the stack names in it.
    const inputs = root ? [root] : paths;
    const args = ['generate', ...(o.strict ? ['--strict'] : []), ...(o.boundary ? ['--boundary', o.boundary] : []), ...(o.args || []), ...inputs];
    const r = spawnSync(binaryPath(), args, { encoding: 'utf8' });
    const byType = resourcesByType(paths);
    const violations = [];
    if (r.error) {
      violations.push(violation('exec-policy-error', 'error', `cfn-exec-policy could not run: ${r.error.message}`, paths.map((p) => ({ templatePath: p }))));
      return { success: false, violations };
    }
    for (const line of (r.stderr || '').split('\n')) {
      let m;
      if ((m = line.match(/^warning: (\S+) (has no handler permissions in the schema; granting .*)$/))) {
        violations.push(violation('exec-policy-guess', o.failOnGuess ? 'error' : 'warning',
          `${m[1]} ${m[2]}: the execution role gets the whole service. Please open an issue on cfn-exec-policy so the type can be measured.`,
          byType.get(m[1]) || [{ templatePath: paths[0] }]));
      } else if ((m = line.match(/^warning: (\S+) is unknown; no permissions granted for it$/))) {
        violations.push(violation('exec-policy-unknown', o.failOnGuess ? 'error' : 'warning',
          `${m[1]} is not in the CloudFormation registry schemas cfn-exec-policy knows, so the execution role gets no permissions for it.`,
          byType.get(m[1]) || [{ templatePath: paths[0] }]));
      } else if ((m = line.match(/^ {2}(.+?): (\S+) (has no PermissionsBoundary|\(.+\) grants permissions to IAM users.*)$/))) {
        violations.push(violation('exec-policy-strict', 'error', `${m[2]} ${m[3]}`, [{ resourceLogicalId: m[2], templatePath: m[1] }]));
      } else if ((m = line.match(/^ {2}(set the boundary on every role in cdk\.json: .*)$/))) {
        // the fix for the roles above; it goes on their violations
        for (const v of violations) if (v.ruleName === 'exec-policy-strict' && v.description.endsWith('has no PermissionsBoundary')) v.fix = m[1];
      }
    }
    if (r.status !== 0 && !violations.some((v) => v.severity === 'error')) {
      violations.push(violation('exec-policy-error', 'error', `cfn-exec-policy generate failed: ${(r.stderr || '').trim()}`, paths.map((p) => ({ templatePath: p }))));
    }
    return { success: violations.every((v) => v.severity !== 'error'), violations };
  }
}

function violation(ruleName, severity, description, violatingResources) {
  return {
    ruleName, severity, description,
    violatingResources: violatingResources.map((x) => ({ resourceLogicalId: x.resourceLogicalId || '', templatePath: x.templatePath, locations: x.locations || [] })),
  };
}

/** Adds ExecPolicyValidation to a CDK app (aws-cdk-lib with Validations). */
class ExecPolicy {
  static apply(scope, options) {
    // The app's own aws-cdk-lib, not one next to this package.
    const { Validations } = require(require.resolve('aws-cdk-lib', { paths: [process.cwd(), __dirname] }));
    if (!Validations) {
      throw new Error('cfn-exec-policy: this aws-cdk-lib has no Validations; pass new ExecPolicyValidation() in new App({ policyValidationBeta1: [...] }) instead');
    }
    Validations.of(scope).addPlugins(new ExecPolicyValidation(options));
  }
}

module.exports = { ExecPolicy, ExecPolicyValidation, binaryPath };
