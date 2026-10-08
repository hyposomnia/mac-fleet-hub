import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';

const source = readFileSync(new URL('./release-fleet-agent.sh', import.meta.url), 'utf8');
const preflight = source.match(/require_notary_credentials\(\) \{[\s\S]*?\n\}/)?.[0];
assert.ok(preflight);

function check(output, exitCode, mode = 'candidate') {
  return spawnSync('bash', ['-c', `
    die() { printf '%s\\n' "$*" >&2; exit 1; }
    xcrun() { printf '%s\\n' "$MOCK_OUTPUT"; return "$MOCK_EXIT"; }
    NOTARY_PROFILE=mac-fleet-hub-notary
    MODE="$MOCK_MODE"
    ${preflight}
    require_notary_credentials
  `], { encoding: 'utf8', timeout: 3000, env: { ...process.env, MOCK_OUTPUT: output, MOCK_EXIT: String(exitCode), MOCK_MODE: mode } });
}

test('notary preflight confirms availability only after a successful command', () => {
  const result = check('Successfully received submission history.', 0);
  assert.equal(result.status, 0);
  assert.match(result.stdout, /可用/);
});

test('a profile lookup failure describes the current session rather than requiring recreation', () => {
  const result = check('No Keychain password item found for profile: mac-fleet-hub-notary', 69);
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /当前.*会话/);
  assert.match(result.stderr, /notarytool history/);
  assert.doesNotMatch(result.stderr, /store-credentials|--password/);
});

test('unexpected notarization errors cannot be reported as valid credentials', () => {
  const result = check('HTTP status 401: authentication failed', 1);
  assert.notEqual(result.status, 0);
  assert.doesNotMatch(result.stdout, /可用/);
});

test('a locked keychain blocks read-only checks as well as actual releases', () => {
  for (const mode of ['check', 'candidate-check', 'candidate', 'release']) {
    const result = check('keychainLocked: User interaction is not allowed.', 69, mode);
    assert.notEqual(result.status, 0, mode);
    assert.doesNotMatch(result.stdout, /可用|跳过/);
  }
});
