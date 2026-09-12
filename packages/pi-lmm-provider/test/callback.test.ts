import assert from 'node:assert/strict';
import { test } from 'node:test';
import { listenCallback } from '../src/callback.ts';
import { LmmError } from '../src/protocol.ts';

const issuer = 'https://api.lmm.best';
const state = 'test-state';

async function callbackRequest(redirectUri: string, query: Record<string, string>): Promise<Response> {
  const url = new URL(redirectUri);
  url.search = `?${new URLSearchParams(query).toString()}`;
  return fetch(url);
}

test('accepts a verified authorization code callback', async () => {
  const callback = await listenCallback(issuer, state, new AbortController().signal);
  const response = await callbackRequest(callback.redirectUri, { state, iss: issuer, code: 'valid-code' });
  assert.equal(response.status, 200);
  assert.equal(await callback.code, 'valid-code');
  callback.close();
});

test('settles promptly on a verified OAuth denial without leaking description', async () => {
  const callback = await listenCallback(issuer, state, new AbortController().signal);
  const response = await callbackRequest(callback.redirectUri, {
    state, iss: issuer, error: 'access_denied', error_description: 'private server detail',
  });
  assert.equal(response.status, 200);
  await assert.rejects(callback.code, (error: unknown) => {
    assert.ok(error instanceof LmmError);
    assert.equal(error.code, 'oauth_denied');
    assert.equal(error.message, 'LMM authorization was denied.');
    assert.doesNotMatch(error.message, /private server detail/);
    return true;
  });
  callback.close();
});

test('ignores forged denial and accepts a later verified code', async () => {
  const callback = await listenCallback(issuer, state, new AbortController().signal);
  const forged = await callbackRequest(callback.redirectUri, { state: 'wrong', iss: issuer, error: 'access_denied' });
  assert.equal(forged.status, 400);
  const pending = callback.code;
  const valid = await callbackRequest(callback.redirectUri, { state, iss: issuer, code: 'valid-after-forgery' });
  assert.equal(valid.status, 200);
  assert.equal(await pending, 'valid-after-forgery');
  callback.close();
});

test('keeps waiting after duplicate and mixed callback parameters', async () => {
  const callback = await listenCallback(issuer, state, new AbortController().signal);
  const duplicateUrl = new URL(callback.redirectUri);
  duplicateUrl.search = `state=${encodeURIComponent(state)}&iss=${encodeURIComponent(issuer)}&error=access_denied&error=access_denied`;
  const duplicateError = await fetch(duplicateUrl);
  assert.equal(duplicateError.status, 400);
  const mixedUrl = new URL(callback.redirectUri);
  mixedUrl.search = new URLSearchParams({ state, iss: issuer, code: 'mixed', error: 'access_denied' }).toString();
  assert.equal((await fetch(mixedUrl)).status, 400);
  const close = callback.code;
  callback.close();
  await assert.rejects(close, { code: 'aborted' });
});

test('aborting cancels the callback and closes the listener', async () => {
  const controller = new AbortController();
  const callback = await listenCallback(issuer, state, controller.signal);
  controller.abort();
  await assert.rejects(callback.code, { code: 'aborted' });
  callback.close();
  await assert.rejects(callbackRequest(callback.redirectUri, { state, iss: issuer, code: 'after-close' }));
});
