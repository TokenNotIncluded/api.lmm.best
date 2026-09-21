import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile, writeFile, readdir } from 'node:fs/promises';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';
import { fixture } from './fixture.mjs';
import { catalog, parseCatalog, selectModel, startBridge, configuration, childEnvironment, runCodewhale } from '../src/provider.mjs';

async function bridgeFixture(t) {
  const f = await fixture(t);
  await f.store.login(f.approve);
  const bridge = await startBridge(f.store);
  t.after(() => bridge.close());
  const request = (path, options = {}) => fetch(bridge.baseURL + path, {
    ...options, headers: { Authorization: `Bearer ${bridge.secret}`, ...options.headers },
  });
  const invoke = (body = {}, headers = {}) => request('/chat/completions', {
    method: 'POST', headers: { 'Content-Type': 'application/json', ...headers },
    body: JSON.stringify({ model: f.modelID, messages: [{ role: 'user', content: 'hello' }], ...body }),
  });
  return { ...f, bridge, request, invoke };
}

test('bridge maps the exact catalog model and group without forwarding caller credentials', async t => {
  const f = await bridgeFixture(t);
  const response = await f.invoke({}, { 'x-api-key': 'DO_NOT_FORWARD', Cookie: 'DO_NOT_FORWARD' });
  assert.equal(response.status, 200);
  assert.equal((await response.json()).choices[0].message.content, 'hello');
  assert.equal(f.state.invocations.length, 1);
  const { headers, body } = f.state.invocations[0];
  assert.equal(body.model, 'example-model');
  assert.equal(headers['x-lmm-group'], f.groupID);
  assert.equal(headers.authorization, 'Bearer lmm_at_access0');
  assert.equal(headers['x-api-key'], undefined);
  assert.equal(headers.cookie, undefined);
  assert.ok(!JSON.stringify(headers).includes(f.bridge.secret));
});
test('bridge requires its random local capability and rejects browser origins', async t => {
  const f = await bridgeFixture(t);
  assert.equal((await fetch(f.bridge.baseURL + '/models')).status, 401);
  assert.equal((await f.request('/models', { headers: { Origin: 'https://example.invalid' } })).status, 401);
  assert.equal((await f.request('/models', { headers: { Authorization: 'Bearer wrong' } })).status, 401);
  assert.equal(f.state.invocations.length, 0);
});
test('only the model-list and Chat Completions paths are exposed', async t => {
  const f = await bridgeFixture(t);
  for (const path of ['/responses', '/messages', '/models?key=secret', '/../api/oauth2/token']) {
    assert.equal((await f.request(path)).status, 404);
  }
  const response = await f.request('/models');
  assert.deepEqual((await response.json()).data.map(m => m.id), [f.modelID]);
});
test('unknown or raw upstream model never falls back to a group', async t => {
  const f = await bridgeFixture(t);
  for (const model of ['example-model', 'lmm:wrong:model', null]) {
    assert.equal((await f.invoke({ model })).status, 502);
  }
  assert.equal(f.state.invocations.length, 0);
});
test('request content type and compressed payload are rejected before inference', async t => {
  const f = await bridgeFixture(t);
  assert.equal((await f.invoke({}, { 'Content-Type': 'text/plain' })).status, 502);
  assert.equal((await f.invoke({}, { 'Content-Encoding': 'gzip' })).status, 502);
  assert.equal(f.state.invocations.length, 0);
});
test('upstream errors preserve status but hide bodies and are never replayed', async t => {
  const f = await bridgeFixture(t);
  f.state.inferenceFailure = true;
  const response = await f.invoke();
  assert.equal(response.status, 429);
  const text = await response.text();
  assert.ok(!text.includes('SECRET_MUST_NOT_LEAK'));
  assert.ok(!text.includes('lmm_at_'));
  assert.equal(f.state.invocations.length, 1);
});
test('SSE arrives before completion and disconnect cancels upstream streaming', async t => {
  const f = await bridgeFixture(t);
  f.state.stream = true;
  const response = await f.invoke({ stream: true });
  assert.match(response.headers.get('content-type'), /text\/event-stream/);
  const reader = response.body.getReader();
  const first = await reader.read();
  assert.equal(first.done, false);
  assert.match(Buffer.from(first.value).toString(), /hello/);
  assert.equal(f.state.streamClosed, false);
  await reader.cancel();
  for (let i = 0; i < 100 && !f.state.streamClosed; i++) await delay(10);
  assert.equal(f.state.streamClosed, true);
});
test('refresh before inference uses the replacement access token', async t => {
  const f = await bridgeFixture(t);
  await f.store.lock(async () => f.store.write({ ...await f.store.read(), expires: Date.now() - 1 }));
  const response = await f.invoke();
  assert.equal(response.status, 200);
  await response.arrayBuffer();
  assert.equal(f.state.refreshes, 1);
  assert.equal(f.state.invocations[0].headers.authorization, 'Bearer lmm_at_access1');
});
test('catalog refuses groups outside the persisted grant and preserves unknown pricing', async t => {
  const f = await fixture(t);
  await f.store.login(f.approve);
  const { session, models } = await catalog(f.store);
  assert.equal(models[0].pricing.input, null);
  const raw = await f.oauth.request('/api/oauth2/catalog', { access: session.access });
  assert.throws(() => parseCatalog(raw, { ...session, scope: 'catalog:read' }), /outside/);
  raw.models[0].apis = ['anthropic-messages', 'openai-responses'];
  assert.deepEqual(parseCatalog(raw, session), []);
});
test('model selection is exact and never chooses the first of multiple models', () => {
  assert.throws(() => selectModel([], undefined));
  assert.throws(() => selectModel([{ id: 'a' }, { id: 'b' }], undefined));
  assert.deepEqual(selectModel([{ id: 'a' }, { id: 'b' }], 'b'), { id: 'b' });
});
test('provider config contains only local capability binding, no LMM credentials', () => {
  const config = configuration('http://127.0.0.1:9999/v1', 'lmm:ZGVmYXVsdA:ZXhhbXBsZQ');
  assert.match(config, /kind = "openai-compatible"/);
  assert.match(config, /api_key_env = "LMM_CODEWHALE_BRIDGE_TOKEN"/);
  assert.ok(!config.includes('lmm_at_'));
  const env = childEnvironment('/private/config.toml', 'LOCAL', { PATH: '/bin', OPENAI_API_KEY: 'WRONG', CODEWHALE_BASE_URL: 'WRONG', DEEPSEEK_HTTP_HEADERS: 'WRONG' });
  assert.equal(env.PATH, '/bin');
  assert.equal(env.CODEWHALE_CONFIG_PATH, '/private/config.toml');
  assert.equal(env.LMM_CODEWHALE_BRIDGE_TOKEN, 'LOCAL');
  assert.equal(env.OPENAI_API_KEY, undefined);
  assert.equal(env.DEEPSEEK_HTTP_HEADERS, undefined);
});
test('missing executable cleans up private per-run config', async t => {
  const f = await fixture(t);
  await f.store.login(f.approve);
  await assert.rejects(runCodewhale(f.store, f.modelID, [], { binary: join(f.home, 'missing-executable') }), /Cannot launch/);
  assert.equal((await readdir(f.home)).filter(p => p.startsWith('run-')).length, 0);
});
test('stub host receives local credentials and makes a real bridge request', { skip: process.platform === 'win32' }, async t => {
  const f = await fixture(t);
  await f.store.login(f.approve);
  const executable = join(f.home, 'codewhale-stub.mjs');
  const receipt = join(f.home, 'receipt.json');
  await writeFile(executable, `#!${process.execPath}\nimport { readFile, writeFile } from 'node:fs/promises';
const config = await readFile(process.env.CODEWHALE_CONFIG_PATH, 'utf8');
const base = JSON.parse(config.match(/^base_url = (.+)$/m)[1]);
const model = JSON.parse(config.match(/^model = (.+)$/m)[1]);
const response = await fetch(base + '/chat/completions', { method:'POST', headers:{ 'Content-Type':'application/json', Authorization:'Bearer ' + process.env.LMM_CODEWHALE_BRIDGE_TOKEN }, body:JSON.stringify({ model, messages:[] }) });
await writeFile(${JSON.stringify(receipt)}, JSON.stringify({args:process.argv.slice(2),config,status:response.status,body:await response.json(),credentialValues:Object.values(process.env).filter(v=>/^lmm_(at|rt)_/.test(v))}));
`, { mode: 0o700 });
  assert.equal(await runCodewhale(f.store, f.modelID, ['exec', 'test prompt'], { binary: executable }), 0);
  const result = JSON.parse(await readFile(receipt));
  assert.deepEqual(result.args, ['--provider', 'lmm', '--model', f.modelID, 'exec', 'test prompt']);
  assert.equal(result.status, 200);
  assert.deepEqual(result.credentialValues, []);
  assert.equal(f.state.invocations.length, 1);
  assert.equal((await readdir(f.home)).filter(p => p.startsWith('run-')).length, 0);
});
