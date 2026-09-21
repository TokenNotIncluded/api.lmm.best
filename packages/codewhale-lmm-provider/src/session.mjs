import { constants } from 'node:fs';
import { mkdir, open, rename, rm, lstat, readFile, realpath } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { homedir } from 'node:os';
import { randomUUID } from 'node:crypto';
import { setTimeout as delay } from 'node:timers/promises';
import { CLIENT_ID, LmmError, check, object, scopes, text, token } from './oauth.mjs';

export function defaultHome(env = process.env) {
  return resolve(env.LMM_CODEWHALE_HOME || join(env.CODEWHALE_HOME || join(homedir(), '.codewhale'), 'lmm-provider'));
}
export class SessionStore {
  constructor(oauth, directory = defaultHome()) {
    this.oauth = oauth;
    this.directory = resolve(directory);
    this.path = join(this.directory, 'session.json');
    this.lockPath = join(this.directory, 'session.lock');
  }
  async prepare() {
    await mkdir(this.directory, { recursive: true, mode: 0o700 });
    const stat = await lstat(this.directory);
    check(stat.isDirectory() && !stat.isSymbolicLink(), 'LMM credential directory must be a real private directory.');
    if (process.platform !== 'win32') {
      check(stat.uid === process.getuid() && (stat.mode & 0o077) === 0, 'LMM credential directory must be owned by you with mode 0700.');
    }
    // Canonicalize system aliases (e.g. macOS /var) once; reject a symlink leaf above.
    this.directory = await realpath(this.directory);
    this.path = join(this.directory, 'session.json');
    this.lockPath = join(this.directory, 'session.lock');
  }
  async lock(fn, signal) {
    await this.prepare();
    const until = Date.now() + 25_000;
    let handle;
    while (!handle) {
      signal?.throwIfAborted();
      try { handle = await open(this.lockPath, 'wx', 0o600); }
      catch (error) {
        if (error.code !== 'EEXIST') throw error;
        check(Date.now() < until, 'LMM session is locked. Close other login processes; after a crash run codewhale-lmm unlock.');
        await delay(40, undefined, { signal });
      }
    }
    try {
      await handle.writeFile(JSON.stringify({ pid: process.pid, id: randomUUID() }));
      await handle.sync();
      return await fn();
    } finally {
      await handle.close();
      await rm(this.lockPath, { force: true });
    }
  }
  async read() {
    let handle;
    try {
      handle = await open(this.path, constants.O_RDONLY | (constants.O_NOFOLLOW || 0));
      const stat = await handle.stat();
      check(stat.isFile() && stat.nlink === 1 && stat.size < 65536, 'Invalid LMM credential file.');
      if (process.platform !== 'win32') check(stat.uid === process.getuid() && (stat.mode & 0o077) === 0, 'LMM credential file must have mode 0600.');
      const value = object(JSON.parse(await handle.readFile('utf8')));
      check(value.version === 1 && value.client_id === CLIENT_ID && value.issuer === this.oauth.issuer && value.resource === this.oauth.resource, 'Stored login belongs to another issuer or client. Use a separate LMM_CODEWHALE_HOME.');
      token(value.access); text(value.refresh); scopes(value.scope);
      check(Number.isSafeInteger(value.expires) && value.expires > 0 && typeof value.refresh_pending === 'boolean');
      return value;
    } catch (error) {
      if (error.code === 'ENOENT') return null;
      if (error instanceof LmmError) throw error;
      throw new LmmError('Cannot read the LMM credential file. It was not overwritten.');
    } finally { await handle?.close(); }
  }
  // One atomic record carries BOTH tokens and the rotation journal marker.
  async write(value) {
    const temp = join(this.directory, `.session-${randomUUID()}.tmp`);
    const handle = await open(temp, 'wx', 0o600);
    try {
      try {
        await handle.writeFile(JSON.stringify(value) + '\n');
        await handle.sync();
      } finally { await handle.close(); }
      await rename(temp, this.path);
      if (process.platform !== 'win32') {
        const directory = await open(this.directory, 'r');
        try { await directory.sync(); } finally { await directory.close(); }
      }
    } finally { await rm(temp, { force: true }); }
  }
  async login(openBrowser, signal) {
    return this.lock(async () => {
      // Validate existing state before replacing it; a different issuer needs its own store.
      const old = await this.read();
      check(!old, 'Already signed in. Run logout (or logout --local-only) before signing in again.');
      const session = await this.oauth.login(openBrowser, signal);
      try { await this.write(session); }
      catch (error) { await this.oauth.revoke(session).catch(() => {}); throw error; }
      return session;
    }, signal);
  }
  async access(signal) {
    return this.lock(async () => {
      const session = await this.read();
      check(session, 'Not signed in. Run codewhale-lmm login.');
      check(!session.refresh_pending, 'A previous token rotation did not commit. Run logout, then login; the old refresh token will not be replayed.');
      if (session.expires > Date.now() + 60_000) return session;
      // Persist this BEFORE the network operation. Ambiguous failure is not retried.
      await this.write({ ...session, refresh_pending: true });
      const replacement = await this.oauth.refresh(session, signal);
      await this.write(replacement);
      return replacement;
    }, signal);
  }
  async status(signal) {
    return this.lock(async () => {
      const session = await this.read();
      return session ? {
        signed_in: true, issuer: session.issuer, client_id: session.client_id,
        expires_at: new Date(session.expires).toISOString(), expired: session.expires <= Date.now(),
        refresh_pending: session.refresh_pending, scopes: scopes(session.scope),
      } : { signed_in: false, issuer: this.oauth.issuer };
    }, signal);
  }
  async logout(localOnly = false, signal) {
    return this.lock(async () => {
      const session = await this.read();
      if (session && !localOnly) await this.oauth.revoke(session, signal);
      // Failed revocation preserves the credential so the user can retry explicitly.
      await rm(this.path, { force: true });
    }, signal);
  }
  async unlock() {
    await this.prepare();
    const before = await lstat(this.lockPath).catch(error => {
      if (error.code === 'ENOENT') return null;
      throw error;
    });
    if (!before) return;
    check(before.isFile() && !before.isSymbolicLink() && before.size < 256);
    let lock;
    try { lock = JSON.parse(await readFile(this.lockPath, 'utf8')); }
    catch { throw new LmmError('Incomplete lock file. Stop all adapter processes and remove session.lock manually.'); }
    check(Number.isSafeInteger(lock.pid) && lock.pid > 0);
    try { process.kill(lock.pid, 0); throw new LmmError('The lock owner is still running. Stop that process first.'); }
    catch (error) { if (error.code !== 'ESRCH') throw error; }
    const after = await lstat(this.lockPath);
    check(after.ino === before.ino && after.mtimeMs === before.mtimeMs, 'Lock changed; it was not removed.');
    await rm(this.lockPath);
  }
}
