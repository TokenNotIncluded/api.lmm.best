import { createServer } from 'node:http';
import { randomBytes, timingSafeEqual } from 'node:crypto';
import { Readable } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { spawn } from 'node:child_process';
import { boundedBody, canonicalID, check, LmmError, object, parseJSON, scopes, text } from './oauth.mjs';

export function parseCatalog(value, session) {
  object(value);
  check(value.schema_version === 1 && value.resource === session.resource && Array.isArray(value.groups) && Array.isArray(value.models));
  check(value.groups.length <= 1000 && value.models.length <= 10000);
  const allowed = new Set(scopes(session.scope));
  const groups = new Map();
  for (const raw of value.groups) {
    const group = object(raw), id = canonicalID(group.id);
    check(!groups.has(id) && id === Buffer.from(text(group.name)).toString('base64url') && group.scope === `group:${id}`);
    check(allowed.has(group.scope), 'Catalog contains a group outside this login grant.');
    groups.set(id, group);
  }
  const models = [], seen = new Set();
  for (const raw of value.models) {
    const model = object(raw), group = groups.get(model.group_id);
    check(group && model.group === group.name);
    text(model.upstream_model); text(model.name);
    check(model.id === `lmm:${group.id}:${Buffer.from(model.upstream_model).toString('base64url')}` && !seen.has(model.id));
    seen.add(model.id);
    check(Array.isArray(model.apis) && model.apis.every(api => typeof api === 'string'));
    // Codewhale's documented named custom-provider interface is Chat Completions.
    // Do not silently route a Responses/Messages-only model through a different wire API.
    if (model.apis.includes('openai-completions')) models.push(model);
  }
  return models;
}
export async function catalog(store, signal) {
  const session = await store.access(signal);
  const raw = await store.oauth.request('/api/oauth2/catalog', { access: session.access, signal });
  return { session, models: parseCatalog(raw, session) };
}
export function selectModel(models, id) {
  if (id) {
    const selected = models.find(model => model.id === id);
    check(selected, 'That model/group is not available on the Chat Completions route. Run models and use an exact ID.');
    return selected;
  }
  check(models.length === 1, 'Choose an explicit model/group with --model <ID>; run codewhale-lmm models to list IDs.');
  return models[0];
}
function equalSecret(value, expected) {
  if (typeof value !== 'string') return false;
  const received = Buffer.from(value), wanted = Buffer.from(expected);
  return received.length === wanted.length && timingSafeEqual(received, wanted);
}
function json(res, status, data) {
  res.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' });
  res.end(JSON.stringify(data));
}

export async function startBridge(store, { timeoutMs = 600_000 } = {}) {
  const secret = randomBytes(32).toString('base64url');
  const active = new Set();
  let authority;
  const server = createServer({ maxHeaderSize: 16384 }, async (req, res) => {
    // No cookie auth, no CORS, no public listener, no caller-selected upstream.
    if (req.headers.host !== authority || req.headers.origin || !equalSecret(req.headers.authorization, `Bearer ${secret}`)) {
      json(res, 401, { error: { message: 'Local bridge authorization required.' } });
      return;
    }
    const list = req.method === 'GET' && req.url === '/v1/models';
    const invoke = req.method === 'POST' && req.url === '/v1/chat/completions';
    if (!list && !invoke) { json(res, 404, { error: { message: 'Unsupported LMM bridge endpoint.' } }); return; }
    const controller = new AbortController();
    const signal = AbortSignal.any([controller.signal, AbortSignal.timeout(timeoutMs)]);
    active.add(controller);
    const disconnect = () => controller.abort();
    res.once('close', disconnect);
    req.once('aborted', disconnect);
    try {
      let body;
      if (invoke) {
        check(req.headers['content-type']?.split(';')[0].trim().toLowerCase() === 'application/json', 'Expected a JSON model request.');
        check(!req.headers['content-encoding'], 'Compressed requests are not supported.');
        body = object(parseJSON(await boundedBody(req, 16_000_000)));
      }
      const { session, models } = await catalog(store, signal);
      if (list) {
        json(res, 200, { object: 'list', data: models.map(model => ({ id: model.id, object: 'model', owned_by: 'lmm' })) });
        return;
      }
      check(scopes(session.scope).includes('models:invoke'), 'This login no longer permits model invocation.');
      const model = selectModel(models, body.model);
      check(body.model === model.id, 'Each request must name an exact model/group ID.');
      // Re-read grants for every request. There is no cross-group fallback and no POST retry.
      const upstream = await store.oauth.fetch(`${store.oauth.issuer}/v1/chat/completions`, {
        method: 'POST', redirect: 'error', signal,
        headers: { Authorization: `Bearer ${session.access}`, 'X-LMM-Group': model.group_id,
          'Content-Type': 'application/json', Accept: 'text/event-stream, application/json' },
        body: JSON.stringify({ ...body, model: model.upstream_model }),
      });
      if (!upstream.ok) {
        await upstream.body?.cancel();
        json(res, upstream.status, { error: { message: `LMM inference rejected (HTTP ${upstream.status}); no request was replayed by the adapter.` } });
        return;
      }
      check(upstream.body, 'LMM returned an empty inference response.');
      const contentType = upstream.headers.get('content-type') || '';
      check(/^(text\/event-stream|application\/json)(;|$)/i.test(contentType), 'LMM returned an unsupported inference content type.');
      res.writeHead(upstream.status, { 'Content-Type': contentType, 'Cache-Control': 'no-store', 'X-Accel-Buffering': 'no' });
      res.flushHeaders();
      await pipeline(Readable.fromWeb(upstream.body), res, { signal });
    } catch (error) {
      // Never echo server bodies, OAuth codes, credentials, or user prompts in diagnostics.
      if (!res.destroyed && !res.headersSent) json(res, 502, { error: { message: error instanceof LmmError ? error.message : 'LMM bridge request failed; it was not replayed.' } });
      else if (!res.destroyed) res.destroy();
    } finally {
      controller.abort();
      active.delete(controller);
      res.removeListener('close', disconnect);
      req.removeListener('aborted', disconnect);
    }
  });
  server.requestTimeout = 30_000;
  server.headersTimeout = 10_000;
  await new Promise((yes, no) => {
    server.once('error', no);
    server.listen(0, '127.0.0.1', () => { server.removeListener('error', no); yes(); });
  });
  authority = `127.0.0.1:${server.address().port}`;
  return { secret, baseURL: `http://${authority}/v1`, async close() {
    for (const controller of active) controller.abort();
    const stopped = new Promise(resolve => server.close(resolve));
    server.closeAllConnections();
    await stopped;
  } };
}

export function configuration(baseURL, model) {
  // JSON quoted strings are a compatible subset of TOML basic strings here.
  text(model);
  return `provider = "lmm"\ndefault_text_model = ${JSON.stringify(model)}\n\n[providers.lmm]\nkind = "openai-compatible"\nbase_url = ${JSON.stringify(baseURL)}\napi_key_env = "LMM_CODEWHALE_BRIDGE_TOKEN"\nmodel = ${JSON.stringify(model)}\n`;
}
export function childEnvironment(configPath, secret, source = process.env) {
  const env = { ...source };
  for (const key of Object.keys(env)) {
    // Keep ambient route overrides from replacing the selected, credential-bound route.
    if (/^(CODEWHALE|DEEPSEEK|OPENAI)_(API_KEY|BASE_URL|MODEL|DEFAULT_TEXT_MODEL|PROVIDER|HTTP_HEADERS|CONFIG_PATH|CONFIG_FILE)$/.test(key)) delete env[key];
  }
  env.CODEWHALE_CONFIG_PATH = configPath;
  env.LMM_CODEWHALE_BRIDGE_TOKEN = secret;
  return env;
}
export async function runCodewhale(store, modelID, args = [], { binary = 'codewhale', signal } = {}) {
  const selected = selectModel((await catalog(store, signal)).models, modelID);
  const bridge = await startBridge(store);
  let directory;
  try {
    directory = await mkdtemp(join(store.directory, 'run-'));
    const configPath = join(directory, 'config.toml');
    await writeFile(configPath, configuration(bridge.baseURL, selected.id), { mode: 0o600 });
    // Only an ephemeral local capability enters the child. LMM OAuth tokens stay in the adapter.
    const child = spawn(binary, ['--provider', 'lmm', '--model', selected.id, ...args], {
      env: childEnvironment(configPath, bridge.secret), stdio: 'inherit', shell: false,
    });
    let killTimer;
    const terminate = () => {
      child.kill('SIGTERM');
      killTimer = setTimeout(() => child.kill('SIGKILL'), 3000);
      killTimer.unref();
    };
    signal?.addEventListener('abort', terminate, { once: true });
    if (signal?.aborted) terminate();
    try {
      return await new Promise((resolve, reject) => {
        child.once('error', () => reject(new LmmError('Cannot launch codewhale. Install the official binary or set LMM_CODEWHALE_BIN.')));
        child.once('exit', (code, exitSignal) => resolve(code ?? (exitSignal === 'SIGINT' ? 130 : 1)));
      });
    } finally { signal?.removeEventListener('abort', terminate); clearTimeout(killTimer); }
  } finally {
    await bridge.close();
    if (directory) await rm(directory, { recursive: true, force: true });
  }
}
