import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';
import { LmmHttp } from '../src/http.ts';
import { LmmOAuth } from '../src/oauth.ts';
import type { LmmCredential } from '../src/protocol.ts';

const scope = 'catalog:read balance:read models:invoke';
function credential(refresh: string): LmmCredential {
  return { type: 'oauth', access: 'lmm_at_old', refresh, expires: Date.now() - 1,
    lmm_issuer: 'http://127.0.0.1:0', lmm_resource: 'http://127.0.0.1:0/api/oauth2', lmm_session: 'session', scope };
}

test('sends at most one request, preserves failed fence, and accepts a later token', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'lmm-refresh-'));
  const server = createServer((_request, response) => {
    response.writeHead(200, { 'content-type': 'application/json' });
    response.end(JSON.stringify({ token_type: 'Bearer', access_token: 'lmm_at_new', refresh_token: 'refresh-new', expires_in: 300, scope }));
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  const issuer = `http://127.0.0.1:${address.port}`;
  const http = new LmmHttp({ issuer, allowLoopbackHttpForTests: true });
  const oauth = new LmmOAuth(http, 180_000, directory);
  let requests = 0;
  server.on('request', () => { requests += 1; });
  try {
    const first = await oauth.refresh({ ...credential('refresh-one'), lmm_issuer: issuer, lmm_resource: `${issuer}/api/oauth2` }, new AbortController().signal);
    assert.equal(first.refresh, 'refresh-new');
    await assert.rejects(oauth.refresh({ ...credential('refresh-one'), lmm_issuer: issuer, lmm_resource: `${issuer}/api/oauth2` }, new AbortController().signal), { code: 'refresh_already_attempted' });
    const second = await oauth.refresh({ ...credential('refresh-two'), lmm_issuer: issuer, lmm_resource: `${issuer}/api/oauth2` }, new AbortController().signal);
    assert.equal(second.refresh, 'refresh-new');
    assert.equal(requests, 2);
  } finally {
    await new Promise<void>((resolve) => server.close(() => resolve()));
    await rm(directory, { recursive: true, force: true });
  }
});

test('does not send a request when the refresh fence cannot be persisted', async () => {
  let requests = 0;
  const fetch = async () => { requests += 1; throw new Error('must not be called'); };
  const http = new LmmHttp({ fetch: fetch as typeof globalThis.fetch });
  const oauth = new LmmOAuth(http, 180_000, join('/dev/null', 'lmm-refresh-file-' + Date.now()));
  const value = { ...credential('refresh-fail'), lmm_issuer: http.issuer, lmm_resource: http.resource };
  await assert.rejects(oauth.refresh(value, new AbortController().signal), { code: 'refresh_storage_unavailable' });
  assert.equal(requests, 0);
});

test('keeps the fence after a failed exchange and never retries that token', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'lmm-refresh-'));
  let requests = 0;
  const fetch = async () => { requests += 1; throw new Error('offline'); };
  const http = new LmmHttp({ fetch: fetch as typeof globalThis.fetch });
  const oauth = new LmmOAuth(http, 180_000, directory);
  const value = { ...credential('refresh-network-failure'), lmm_issuer: http.issuer, lmm_resource: http.resource };
  try {
    await assert.rejects(oauth.refresh(value, new AbortController().signal), { code: 'transport_error' });
    await assert.rejects(oauth.refresh(value, new AbortController().signal), { code: 'refresh_already_attempted' });
    assert.equal(requests, 1);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
