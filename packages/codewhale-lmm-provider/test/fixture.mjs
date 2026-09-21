import { createServer } from 'node:http';
import { createHash } from 'node:crypto';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { OAuth, SCOPES, CLIENT_ID } from '../src/oauth.mjs';
import { SessionStore } from '../src/session.mjs';

export async function fixture(t) {
  const home = await mkdtemp(join(tmpdir(), 'codewhale-lmm-test-'));
  const groupID = Buffer.from('default').toString('base64url');
  const modelID = `lmm:${groupID}:${Buffer.from('example-model').toString('base64url')}`;
  const scope = [...SCOPES, `group:${groupID}`].join(' ');
  const state = { authorizations: new Map(), refreshes: 0, invocations: [], revocations: 0, expires: 3600,
    refreshFailure: false, revokeFailure: false, inferenceFailure: false, stream: false, streamClosed: false, grants: scope };
  let issuer;
  const json = (res, status, data) => { res.writeHead(status, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(data)); };
  const tokenResponse = n => ({ token_type: 'Bearer', access_token: `lmm_at_access${n}`, refresh_token: `lmm_rt_refresh${n}`, expires_in: state.expires, scope: state.grants });
  const server = createServer(async (req, res) => {
    const url = new URL(req.url, issuer);
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    const raw = Buffer.concat(chunks).toString('utf8');
    if (url.pathname === '/.well-known/oauth-authorization-server') {
      json(res, 200, { issuer, authorization_endpoint: `${issuer}/api/oauth2/authorize`, token_endpoint: `${issuer}/api/oauth2/token`,
        revocation_endpoint: `${issuer}/api/oauth2/revoke`, code_challenge_methods_supported: ['S256'], response_types_supported: ['code'], authorization_response_iss_parameter_supported: true });
    } else if (url.pathname === '/.well-known/oauth-protected-resource/api/oauth2') {
      json(res, 200, { resource: `${issuer}/api/oauth2`, authorization_servers: [issuer] });
    } else if (url.pathname === '/api/oauth2/authorize') {
      const params = url.searchParams;
      if (params.get('client_id') !== CLIENT_ID || params.get('scope') !== SCOPES.join(' ') || params.get('resource') !== `${issuer}/api/oauth2`) { json(res, 400, {}); return; }
      state.lastAuthorization = params;
      const code = `test-code-${state.authorizations.size}`;
      state.authorizations.set(code, params);
      const redirect = new URL(params.get('redirect_uri'));
      redirect.search = new URLSearchParams({ code, state: params.get('state'), iss: issuer }).toString();
      res.writeHead(302, { Location: redirect.href }).end();
    } else if (url.pathname === '/api/oauth2/token') {
      const body = new URLSearchParams(raw);
      if (body.get('client_id') !== CLIENT_ID || body.get('resource') !== `${issuer}/api/oauth2`) { json(res, 400, {}); return; }
      if (body.get('grant_type') === 'authorization_code') {
        const auth = state.authorizations.get(body.get('code'));
        if (!auth || auth.get('redirect_uri') !== body.get('redirect_uri') || auth.get('code_challenge') !== createHash('sha256').update(body.get('code_verifier')).digest('base64url')) { json(res, 400, {}); return; }
        state.authorizations.delete(body.get('code'));
        json(res, 200, tokenResponse(0));
      } else {
        state.refreshes++;
        if (state.refreshFailure) { json(res, 503, { error: 'SECRET_MUST_NOT_LEAK' }); return; }
        if (body.get('refresh_token') !== `lmm_rt_refresh${state.refreshes - 1}`) { json(res, 400, {}); return; }
        await new Promise(resolve => setTimeout(resolve, 20));
        json(res, 200, tokenResponse(state.refreshes));
      }
    } else if (url.pathname === '/api/oauth2/catalog') {
      if (!req.headers.authorization?.startsWith('Bearer lmm_at_')) { json(res, 401, {}); return; }
      json(res, 200, { schema_version: 1, resource: `${issuer}/api/oauth2`, groups: [{ id: groupID, name: 'default', scope: `group:${groupID}` }],
        models: [{ id: modelID, group_id: groupID, group: 'default', upstream_model: 'example-model', name: 'Example model', apis: ['openai-completions'], pricing: { input: null, output: null } }] });
    } else if (url.pathname === '/api/oauth2/revoke') {
      if (state.revokeFailure) { json(res, 503, { error: 'SECRET_MUST_NOT_LEAK' }); return; }
      state.revocations++;
      res.writeHead(200).end();
    } else if (url.pathname === '/v1/chat/completions') {
      state.invocations.push({ headers: req.headers, body: JSON.parse(raw) });
      if (state.inferenceFailure) { json(res, 429, { error: 'SECRET_MUST_NOT_LEAK' }); return; }
      if (state.stream) {
        res.writeHead(200, { 'Content-Type': 'text/event-stream' });
        res.write('data: {"choices":[{"delta":{"content":"hello"}}]}\n\n');
        const timer = setInterval(() => res.write(': heartbeat\n\n'), 50);
        res.on('close', () => { clearInterval(timer); state.streamClosed = true; });
      } else json(res, 200, { choices: [{ message: { role: 'assistant', content: 'hello' } }] });
    } else json(res, 404, {});
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  issuer = `http://127.0.0.1:${server.address().port}`;
  const oauth = new OAuth(issuer), store = new SessionStore(oauth, home);
  const approve = async url => {
    const response = await fetch(url, { redirect: 'manual' });
    if (response.status !== 302) throw new Error('Mock consent failed');
    await fetch(response.headers.get('location'));
  };
  t.after(async () => {
    server.closeAllConnections();
    await new Promise(resolve => server.close(resolve));
    await rm(home, { recursive: true, force: true });
  });
  return { state, issuer, oauth, store, home, approve, modelID, groupID, scope };
}
