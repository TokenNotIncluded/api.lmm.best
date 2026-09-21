import { createHash, randomBytes } from 'node:crypto';
import { createServer } from 'node:http';

export const CLIENT_ID = 'lmm-codewhale';
export const SCOPES = ['catalog:read', 'balance:read', 'usage:read', 'models:invoke'];
export const CALLBACK_PATH = '/oauth/lmm/callback';
export class LmmError extends Error {}
export function check(condition, message = 'Invalid LMM response.') {
  if (!condition) throw new LmmError(message);
}
export function safeMessage(error) {
  return error instanceof LmmError ? error.message : 'LMM operation failed. No credential was printed.';
}
export function text(value, max = 4096) {
  check(typeof value === 'string' && value.length > 0 && value.length <= max && !/[\p{Cc}\p{Cf}]/u.test(value));
  return value;
}
export function issuerURL(value) {
  let url;
  try { url = new URL(value); } catch { throw new LmmError('Invalid LMM issuer URL.'); }
  check(url.protocol === 'https:' || (url.protocol === 'http:' && url.hostname === '127.0.0.1'), 'LMM requires HTTPS (127.0.0.1 HTTP is allowed for local tests).');
  check(!url.username && !url.password && !url.search && !url.hash && url.pathname === '/', 'Use an issuer origin, without a path, credentials, query or fragment.');
  return url.origin;
}
export function token(value) {
  check(typeof value === 'string' && /^lmm_at_[A-Za-z0-9_-]{1,4089}$/.test(value), 'Expected an LMM OAuth access token, not an API key.');
  return value;
}
export function scopes(value) {
  const list = text(value, 16384).split(' ');
  check(new Set(list).size === list.length);
  for (const scope of list) {
    if (SCOPES.includes(scope)) continue;
    check(scope.startsWith('group:'));
    canonicalID(scope.slice(6));
  }
  return list;
}
export function canonicalID(value) {
  text(value);
  check(/^[A-Za-z0-9_-]+$/.test(value));
  const decoded = Buffer.from(value, 'base64url').toString('utf8');
  text(decoded);
  check(Buffer.from(decoded).toString('base64url') === value);
  return value;
}
export async function boundedBody(body, limit = 2_000_000) {
  let size = 0;
  const parts = [];
  for await (const part of body) {
    const bytes = Buffer.from(part);
    size += bytes.length;
    check(size <= limit, 'Response or request exceeds the size limit.');
    parts.push(bytes);
  }
  return Buffer.concat(parts);
}
export function parseJSON(bytes) {
  try { return JSON.parse(bytes.toString('utf8')); } catch { throw new LmmError('Invalid JSON response or request.'); }
}
export function object(value) {
  check(value && typeof value === 'object' && !Array.isArray(value));
  return value;
}

// The listener exists before the browser is opened. Bad callbacks never consume a login.
export async function listenCallback(issuer, state, signal) {
  signal?.throwIfAborted();
  let resolve, reject;
  const code = new Promise((yes, no) => { resolve = yes; reject = no; });
  code.catch(() => {});
  let settled = false, redirectUri;
  const finish = (error, value) => {
    if (settled) return;
    settled = true;
    error ? reject(error) : resolve(value);
  };
  const server = createServer({ maxHeaderSize: 8192 }, (req, res) => {
    res.setHeader('Cache-Control', 'no-store');
    res.setHeader('Referrer-Policy', 'no-referrer');
    res.setHeader('Content-Security-Policy', "default-src 'none'; frame-ancestors 'none'");
    res.setHeader('Content-Type', 'text/plain; charset=utf-8');
    try {
      check(!settled && req.method === 'GET' && req.headers.host === new URL(redirectUri).host && !req.headers.origin);
      check(req.url?.startsWith('/') && !req.url.startsWith('//') && req.url.length < 8192);
      const url = new URL(req.url, redirectUri);
      check(url.origin === new URL(redirectUri).origin && url.pathname === CALLBACK_PATH && !url.hash);
      const params = url.searchParams;
      for (const key of ['state', 'iss']) check(params.getAll(key).length === 1);
      check(params.get('state') === state && params.get('iss') === issuer);
      check(params.getAll('code').length + params.getAll('error').length === 1);
      if (params.has('error')) {
        res.writeHead(400).end('Authorization was declined. Return to your terminal.');
        finish(new LmmError('LMM authorization was declined. Run login again.'));
        return;
      }
      const value = text(params.get('code'));
      res.end('LMM login approved. Return to Codewhale.');
      finish(null, value);
    } catch { res.writeHead(400).end('Invalid OAuth callback.'); }
  });
  server.requestTimeout = 5000;
  server.headersTimeout = 5000;
  const abort = () => {
    finish(new LmmError('LMM login cancelled or timed out. Run login again.'));
    server.close();
    server.closeAllConnections();
  };
  server.on('error', () => finish(new LmmError('Cannot start the local OAuth callback listener.')));
  await new Promise((yes, no) => {
    server.once('error', no);
    server.listen(0, '127.0.0.1', () => { server.removeListener('error', no); yes(); });
  });
  redirectUri = `http://127.0.0.1:${server.address().port}${CALLBACK_PATH}`;
  signal?.addEventListener('abort', abort, { once: true });
  if (signal?.aborted) abort();
  return {
    code, redirectUri,
    close() {
      signal?.removeEventListener('abort', abort);
      finish(new LmmError('LMM login was closed.'));
      server.close();
      server.closeAllConnections();
    },
  };
}

export class OAuth {
  constructor(issuer = 'https://api.lmm.best', fetchImpl = fetch) {
    this.issuer = issuerURL(issuer);
    this.resource = `${this.issuer}/api/oauth2`;
    this.fetch = fetchImpl;
  }
  async request(path, { access, form, signal } = {}) {
    check(path.startsWith('/') && !path.startsWith('//'));
    const response = await this.fetch(`${this.issuer}${path}`, {
      method: form ? 'POST' : 'GET', redirect: 'error',
      headers: {
        Accept: 'application/json',
        ...(access ? { Authorization: `Bearer ${token(access)}` } : {}),
        ...(form ? { 'Content-Type': 'application/x-www-form-urlencoded' } : {}),
      },
      body: form ? new URLSearchParams(form) : undefined,
      signal: AbortSignal.any([AbortSignal.timeout(20_000), ...(signal ? [signal] : [])]),
    });
    if (!response.ok) {
      await response.body?.cancel();
      throw new LmmError(response.status === 401 ? 'LMM authorization expired or was revoked. Run login again.' : `LMM request rejected (HTTP ${response.status}).`);
    }
    const bytes = response.body ? await boundedBody(response.body) : Buffer.alloc(0);
    return bytes.length ? object(parseJSON(bytes)) : {};
  }
  async discover(signal) {
    const [auth, resource] = await Promise.all([
      this.request('/.well-known/oauth-authorization-server', { signal }),
      this.request('/.well-known/oauth-protected-resource/api/oauth2', { signal }),
    ]);
    check(auth.issuer === this.issuer && auth.authorization_endpoint === `${this.resource}/authorize` &&
      auth.token_endpoint === `${this.resource}/token` && auth.revocation_endpoint === `${this.resource}/revoke`, 'OAuth discovery does not match the configured LMM issuer.');
    check(auth.authorization_response_iss_parameter_supported === true &&
      Array.isArray(auth.code_challenge_methods_supported) && auth.code_challenge_methods_supported.includes('S256') &&
      Array.isArray(auth.response_types_supported) && auth.response_types_supported.includes('code'), 'LMM must support PKCE S256 and issuer-bound authorization responses.');
    check(resource.resource === this.resource && Array.isArray(resource.authorization_servers) &&
      resource.authorization_servers.length === 1 && resource.authorization_servers[0] === this.issuer, 'Protected-resource metadata does not match LMM.');
  }
  parseTokens(response, startedAt, previous) {
    check(response.token_type === 'Bearer');
    const access = token(response.access_token);
    const refresh = text(response.refresh_token);
    check(!/\s/.test(refresh) && refresh !== access);
    check(Number.isSafeInteger(response.expires_in) && response.expires_in > 0 && response.expires_in <= 86400);
    const scope = response.scope ?? previous?.scope;
    const granted = scopes(scope);
    if (previous) {
      const old = new Set(scopes(previous.scope));
      check(granted.every(s => old.has(s)) && refresh !== previous.refresh, 'Refresh must rotate the token and must not widen scopes. Run login again.');
    } else {
      check(SCOPES.every(s => granted.includes(s)), 'LMM did not grant the required application permissions.');
    }
    return { version: 1, client_id: CLIENT_ID, issuer: this.issuer, resource: this.resource, access, refresh, scope, expires: startedAt + response.expires_in * 1000, refresh_pending: false };
  }
  async login(openBrowser, signal = AbortSignal.timeout(180_000)) {
    await this.discover(signal);
    const verifier = randomBytes(32).toString('base64url');
    const state = randomBytes(32).toString('base64url');
    const callback = await listenCallback(this.issuer, state, signal);
    try {
      const url = new URL(`${this.resource}/authorize`);
      url.search = new URLSearchParams({ client_id: CLIENT_ID, response_type: 'code', redirect_uri: callback.redirectUri,
        scope: SCOPES.join(' '), resource: this.resource, state, code_challenge_method: 'S256',
        code_challenge: createHash('sha256').update(verifier).digest('base64url') }).toString();
      await openBrowser(url.href);
      const code = await callback.code;
      callback.close();
      const startedAt = Date.now();
      const response = await this.request('/api/oauth2/token', { signal, form: {
        grant_type: 'authorization_code', client_id: CLIENT_ID, code, redirect_uri: callback.redirectUri,
        code_verifier: verifier, resource: this.resource,
      } });
      return this.parseTokens(response, startedAt);
    } finally { callback.close(); }
  }
  async refresh(session, signal) {
    const startedAt = Date.now();
    const response = await this.request('/api/oauth2/token', { signal, form: {
      grant_type: 'refresh_token', client_id: CLIENT_ID, refresh_token: session.refresh, resource: this.resource,
    } });
    return this.parseTokens(response, startedAt, session);
  }
  async revoke(session, signal) {
    await this.request('/api/oauth2/revoke', { signal, form: {
      client_id: CLIENT_ID, token: token(session.access), token_type_hint: 'access_token',
    } });
  }
}
