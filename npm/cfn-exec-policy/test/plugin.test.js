// node --test npm/cfn-exec-policy/test/plugin.test.js (CFN_EXEC_POLICY_BINARY: a built cfn-exec-policy)
const test = require('node:test');
const assert = require('node:assert');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { ExecPolicyValidation } = require('..');

// A one-stack cloud assembly with a role without a boundary and an unknown type.
function assembly() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'cep-'));
  fs.writeFileSync(path.join(dir, 'manifest.json'), JSON.stringify({ version: '36.0.0', artifacts: {
    App: { type: 'aws:cloudformation:stack', environment: 'aws://111111111111/us-east-1', properties: { templateFile: 'App.template.json' } },
  } }));
  const template = path.join(dir, 'App.template.json');
  fs.writeFileSync(template, JSON.stringify({ Resources: {
    Role: { Type: 'AWS::IAM::Role', Properties: { AssumeRolePolicyDocument: {} } },
    Thing: { Type: 'Third::Party::Thing' },
  } }));
  return template;
}

test('an unknown type is a warning at its resource', () => {
  const template = assembly();
  const r = new ExecPolicyValidation().validate({ templatePaths: [template] });
  assert.equal(r.success, true);
  assert.equal(r.violations.length, 1);
  assert.equal(r.violations[0].ruleName, 'exec-policy-unknown');
  assert.deepEqual(r.violations[0].violatingResources[0].resourceLogicalId, 'Thing');
  assert.equal(new ExecPolicyValidation({ failOnGuess: true }).validate({ templatePaths: [template] }).success, false);
});

test('strict: a role without the boundary fails, with the fix', () => {
  const r = new ExecPolicyValidation({ strict: true }).validate({ stackTemplates: [{ templatePath: assembly() }] });
  assert.equal(r.success, false);
  const v = r.violations.find((x) => x.ruleName === 'exec-policy-strict');
  assert.ok(v, JSON.stringify(r.violations));
  assert.equal(v.violatingResources[0].resourceLogicalId, 'Role');
  assert.match(v.fix, /@aws-cdk\/core:permissionsBoundary/);
});
