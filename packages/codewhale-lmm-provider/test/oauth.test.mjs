import test from 'node:test';
import assert from 'node:assert/strict';
import { OAuth, CLIENT_ID, issuerURL, listenCallback, safeMessage } from '../src/oauth.mjs';
import { fixture } from './fixture.mjs';

test('OAuth authorization-code flow binds client, resource, state, issuer and PKCE', async t => {
  const f = await fixture(t);
  const session = await f.store.login(f.approve);
  assert.equal(session.client_id, CLIENT_ID);
  assert.equal(session.access, 'lmm_at_access0');
  assert.equal(session.scope, f.scope);
  assert.equal(f.state.lastAuthorization.get('code_challenge_method'), 'S256');
  assert.equal(f.state.lastAuthorization.get('code_challenge').length, 43);
  assert.equal(f.state.lastAuthorization.get('state').length, 43);
});

test('callback rejects wrong state/issuer and duplicate fields without consuming login', async () => {
  const callback = await listenCallback('https://api.lmm.test', 'expected-state', AbortSignal.timeout(3000));
  try {
    for (const query of [
      'code=secret&state=wrong&iss=https://api.lmm.test',
      'code=secret&state=expected-state&iss=https://attacker.test',
      'code=secret&code=second&state=expected-state&iss=https://api.lmm.test',
      'code=secret&state=expected-state&state=second&iss=https://api.lmm.test',
      'code=secret&error=access_denied&state=expected-state&iss=https://api.lmm.test',
    ]) {
      const response = await fetch(`${callback.redirectUri}?${query}`);
      assert.equal(response.status, 400);
      assert.equal((await response.text()).includes('secret'), false);
    }
    const good = new URL(callback.redirectUri);
    good.search = new URLSearchParams({ code: 'good-code', state: 'expected-state', iss: 'https://api.lmm.test' }).toString();
    assert.equal((await fetch(good)).status, 200);
    assert.equal(await callback.code, 'good-code');
  } finally { callback.close(); }
});

test('callback cancellation closes the listener', async () => {
  const controller = new AbortController();
  const callback = await listenCallback('https://api.lmm.test', 's', controller.signal);
  controller.abort();
  await assert.rejects(callback.code, /cancelled/);
  await assert.rejects(fetch(callback.redirectUri));
  callback.close();
});

test('issuer rejects plaintext remote servers and origin confusion', () => {
  for (const value of ['http://evil.test', 'https://u:p@api.test', 'https://api.test/path', 'https://api.test/?x=1', 'https://api.test/#x']) assert.throws(() => issuerURL(value));
  assert.equal(issuerURL('https://API.TEST/'), 'https://api.test');
});

test('discovery endpoint mismatch fails before opening a browser', async () => {
  let opened = false;
  const oauth = new OAuth('https://api.test', async () => new Response(JSON.stringify({ issuer: 'https://evil.test' })));
  await assert.rejects(oauth.login(() => { opened = true; }), /discovery/);
  assert.equal(opened, false);
});

test('HTTP credentials are not forwarded across redirects', async t => {
  const f = await fixture(t);
  let options;
  const oauth = new OAuth(f.issuer, async (_url, value) => { options = value; return new Response('{}'); });
  await oauth.request('/api/oauth2/balance', { access: 'lmm_at_test' });
  assert.equal(options.redirect, 'error');
  assert.equal(options.headers.Authorization, 'Bearer lmm_at_test');
});

test('token responses cannot widen scope or reuse a refresh token', async t => {
  const f = await fixture(t), session = await f.store.login(f.approve);
  const response = { token_type: 'Bearer', access_token: 'lmm_at_next', refresh_token: 'lmm_rt_next', expires_in: 3600, scope: `${f.scope} group:b3RoZXI` };
  assert.throws(() => f.oauth.parseTokens(response, Date.now(), session), /widen/);
  response.scope = f.scope; response.refresh_token = session.refresh;
  assert.throws(() => f.oauth.parseTokens(response, Date.now(), session), /rotate/);
  response.refresh_token = 'lmm_rt_next'; delete response.scope;
  assert.equal(f.oauth.parseTokens(response, Date.now(), session).scope, session.scope);
});

test('unknown exceptions are redacted', () => {
  assert.equal(safeMessage(new Error('lmm_at_secret')), 'LMM operation failed. No credential was printed.');
});
