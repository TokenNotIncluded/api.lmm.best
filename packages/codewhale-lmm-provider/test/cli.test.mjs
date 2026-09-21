import test from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtemp, symlink, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const cli = fileURLToPath(new URL('../src/cli.mjs', import.meta.url));
test('CLI direct entrypoint prints help without touching account credentials', () => {
  assert.match(execFileSync(process.execPath, [cli, '--help'], { encoding: 'utf8' }), /codewhale-lmm login/);
});
test('npm-style symlink entrypoint actually executes the CLI', { skip: process.platform === 'win32' }, async t => {
  const directory = await mkdtemp(join(tmpdir(), 'codewhale-bin-test-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const bin = join(directory, 'codewhale-lmm');
  await symlink(cli, bin);
  assert.match(execFileSync(bin, ['--help'], { encoding: 'utf8' }), /codewhale-lmm login/);
});
test('unknown arguments fail without leaking argument values through exceptions', () => {
  const result = spawnSync(process.execPath, [cli, '--FAKE_SECRET_MUST_NOT_LEAK'], { encoding: 'utf8' });
  assert.equal(result.status, 1);
  assert.doesNotMatch(result.stderr, /FAKE_SECRET_MUST_NOT_LEAK/);
  assert.match(result.stderr, /No credential was printed/);
});
