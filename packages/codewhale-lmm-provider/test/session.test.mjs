import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile, stat, writeFile, symlink } from 'node:fs/promises';
import { join } from 'node:path';
import { spawn } from 'node:child_process';
import { SessionStore } from '../src/session.mjs';
import { OAuth } from '../src/oauth.mjs';
import { fixture } from './fixture.mjs';

async function expire(f) {
  const session = await f.store.read();
  await f.store.write({ ...session, expires: Date.now() - 1 });
}

test('status never reveals tokens or refreshes credentials', async t => {
  const f = await fixture(t);
  assert.equal((await f.store.status()).signed_in, false);
  await f.store.login(f.approve); await expire(f);
  const status = await f.store.status();
  assert.equal(status.expired, true);
  assert.equal(JSON.stringify(status).includes('lmm_at_'), false);
  assert.equal(JSON.stringify(status).includes('lmm_rt_'), false);
  assert.equal(f.state.refreshes, 0);
});

test('owner-only credential storage on POSIX', { skip: process.platform === 'win32' }, async t => {
  const f = await fixture(t); await f.store.login(f.approve);
  assert.equal((await stat(f.home)).mode & 0o777, 0o700);
  assert.equal((await stat(f.store.path)).mode & 0o777, 0o600);
});

test('concurrent refreshes serialize and persist one replacement', async t => {
  const f = await fixture(t); await f.store.login(f.approve); await expire(f);
  const other = new SessionStore(f.oauth, f.home);
  const sessions = await Promise.all([f.store.access(), other.access(), f.store.access(), other.access()]);
  assert.equal(f.state.refreshes, 1);
  assert.ok(sessions.every(session => session.access === 'lmm_at_access1'));
  assert.equal((await f.store.read()).refresh_pending, false);
});

test('separate Node processes serialize refreshes using the same file lock', async t => {
  const f = await fixture(t); await f.store.login(f.approve); await expire(f);
  const code = `import {OAuth} from ${JSON.stringify(new URL('../src/oauth.mjs', import.meta.url).href)};\nimport {SessionStore} from ${JSON.stringify(new URL('../src/session.mjs', import.meta.url).href)};\nawait new SessionStore(new OAuth(process.env.TEST_ISSUER), process.env.TEST_HOME).access();`;
  const child = () => new Promise((resolve, reject) => {
    const proc = spawn(process.execPath, ['--input-type=module', '-e', code], { env: { ...process.env, TEST_HOME: f.home, TEST_ISSUER: f.issuer }, stdio: 'ignore' });
    proc.once('error', reject); proc.once('exit', code => code === 0 ? resolve() : reject(new Error('Child failed')));
  });
  await Promise.all([child(), child()]);
  assert.equal(f.state.refreshes, 1);
});

test('ambiguous refresh failure persists journal and prevents token replay', async t => {
  const f = await fixture(t); await f.store.login(f.approve); await expire(f);
  f.state.refreshFailure = true;
  await assert.rejects(f.store.access(), /HTTP 503/);
  assert.equal((await f.store.read()).refresh_pending, true);
  f.state.refreshFailure = false;
  await assert.rejects(f.store.access(), /rotation did not commit/);
  assert.equal(f.state.refreshes, 1);
});

test('crash marker blocks refresh even in a newly constructed adapter', async t => {
  const f = await fixture(t); await f.store.login(f.approve);
  await f.store.write({ ...await f.store.read(), refresh_pending: true });
  await assert.rejects(new SessionStore(f.oauth, f.home).access(), /rotation did not commit/);
  assert.equal(f.state.refreshes, 0);
});

test('failed revocation keeps local credentials; local-only deletion is explicit', async t => {
  const f = await fixture(t); await f.store.login(f.approve);
  f.state.revokeFailure = true;
  await assert.rejects(f.store.logout(), /HTTP 503/);
  assert.ok(await f.store.read());
  await f.store.logout(true);
  assert.equal(await f.store.read(), null);
  assert.equal(f.state.revocations, 0);
});

test('logout revokes the token family and then deletes local credentials', async t => {
  const f = await fixture(t); await f.store.login(f.approve); await f.store.logout();
  assert.equal(f.state.revocations, 1);
  assert.equal(await f.store.read(), null);
});

test('different issuers cannot consume or replace the same login', async t => {
  const f = await fixture(t); await f.store.login(f.approve);
  const other = new SessionStore(new OAuth('https://another.test'), f.home);
  await assert.rejects(other.access(), /another issuer/);
  await assert.rejects(other.login(() => {}), /another issuer/);
  assert.equal((await f.store.read()).issuer, f.issuer);
});

test('corrupt storage is preserved rather than silently overwritten', async t => {
  const f = await fixture(t); await f.store.prepare();
  await writeFile(f.store.path, 'not-json', { mode: 0o600 });
  await assert.rejects(f.store.login(() => {}), /not overwritten/);
  assert.equal(await readFile(f.store.path, 'utf8'), 'not-json');
});

test('unlock refuses a live process lock', async t => {
  const f = await fixture(t); await f.store.prepare();
  await writeFile(f.store.lockPath, JSON.stringify({ pid: process.pid }), { mode: 0o600 });
  await assert.rejects(f.store.unlock(), /still running/);
});

test('credential directory symlinks are rejected', { skip: process.platform === 'win32' }, async t => {
  const f = await fixture(t);
  const alias = join(f.home, 'alias'); await symlink(f.home, alias);
  await assert.rejects(new SessionStore(f.oauth, alias).prepare(), /real private directory/);
});
