import { createHash, randomBytes, randomUUID } from 'node:crypto';
import type { OAuthCredential, ProviderAuthInteraction } from '@earendil-works/pi-ai';
import { listenCallback } from './callback.ts';
import type { LmmHttp } from './http.ts';
import { RefreshJournal } from './refresh-journal.ts';
import {
  CLIENT_ID, INITIAL_SCOPES, LmmError, accessToken, boundedSignal, credential, nonnegative,
  parseScope, requireValue, text, type LmmCredential,
} from './protocol.ts';

export const REFRESH_BLOCKED = 'Automatic LMM refresh requires a configured durable refresh journal. Use /login.';

export class LmmOAuth {
  readonly http: LmmHttp;
  readonly loginTimeoutMs: number;
  readonly refreshJournal?: RefreshJournal;

  constructor(http: LmmHttp, loginTimeoutMs = 180_000, refreshJournalDirectory?: string) {
    this.http = http;
    this.loginTimeoutMs = loginTimeoutMs;
    this.refreshJournal = refreshJournalDirectory === undefined ? undefined : new RefreshJournal(refreshJournalDirectory);
  }

  async discover(signal: AbortSignal): Promise<void> {
    const [authorization, resource] = await Promise.all([
      this.http.request('/.well-known/oauth-authorization-server', { signal }, 64_000),
      this.http.request('/.well-known/oauth-protected-resource/api/oauth2', { signal }, 64_000),
    ]);
    requireValue(authorization.issuer === this.http.issuer &&
      authorization.authorization_endpoint === `${this.http.resource}/authorize` &&
      authorization.token_endpoint === `${this.http.resource}/token` &&
      authorization.revocation_endpoint === `${this.http.resource}/revoke`, 'LMM discovery issuer or endpoints do not match the configured issuer.');
    requireValue(Array.isArray(authorization.code_challenge_methods_supported) && authorization.code_challenge_methods_supported.includes('S256'));
    requireValue(Array.isArray(authorization.response_types_supported) && authorization.response_types_supported.includes('code'));
    requireValue(authorization.authorization_response_iss_parameter_supported === true, 'LMM discovery must advertise issuer-bound authorization responses.');
    requireValue(resource.resource === this.http.resource && Array.isArray(resource.authorization_servers) &&
      resource.authorization_servers.length === 1 && resource.authorization_servers[0] === this.http.issuer,
      'LMM protected-resource metadata does not match the configured resource.');
  }

  async login(interaction: ProviderAuthInteraction): Promise<LmmCredential> {
    const signal = boundedSignal(interaction.signal, this.loginTimeoutMs);
    await this.discover(signal);
    const verifier = randomBytes(32).toString('base64url');
    const state = randomBytes(32).toString('base64url');
    const challenge = createHash('sha256').update(verifier).digest('base64url');
    const callback = await listenCallback(this.http.issuer, state, signal);
    try {
      const url = new URL(`${this.http.resource}/authorize`);
      url.search = new URLSearchParams({
        client_id: CLIENT_ID, response_type: 'code', redirect_uri: callback.redirectUri,
        scope: INITIAL_SCOPES.join(' '), resource: this.http.resource,
        code_challenge: challenge, code_challenge_method: 'S256', state,
      }).toString();
      // Pi's native OAuth UI owns opening the browser. The listener is already bound.
      interaction.notify({ type: 'auth_url', url: url.href, instructions: 'Sign in and approve LMM in your browser, then return to Pi. Cancel in Pi to stop waiting.' });
      const code = await callback.code;
      callback.close();
      signal.throwIfAborted();
      const startedAt = Date.now();
      const response = await this.http.form('/api/oauth2/token', {
        grant_type: 'authorization_code', client_id: CLIENT_ID, code,
        redirect_uri: callback.redirectUri, code_verifier: verifier, resource: this.http.resource,
      }, signal);
      return this.parseToken(response, startedAt);
    } finally {
      callback.close();
    }
  }

  private parseToken(response: Record<string, unknown>, startedAt: number, previous?: LmmCredential): LmmCredential {
    requireValue(response.token_type === 'Bearer');
    const access = accessToken(response.access_token);
    const refresh = text(response.refresh_token, 4096);
    requireValue(!/\s/.test(refresh) && refresh !== access);
    const lifetime = nonnegative(response.expires_in);
    requireValue(Number.isSafeInteger(lifetime) && lifetime > 0 && lifetime <= 86400);
    const scope = parseScope(response.scope === undefined ? previous?.scope : response.scope);
    const scopes = new Set(scope.split(' '));
    if (previous) {
      const oldScopes = new Set(previous.scope.split(' '));
      requireValue([...scopes].every((entry) => oldScopes.has(entry)), 'LMM refresh tried to widen granted scope. Sign in again.');
      requireValue(refresh !== previous.refresh, 'LMM refresh did not rotate the refresh token. Sign in again.');
    } else {
      requireValue(INITIAL_SCOPES.every((entry) => scopes.has(entry)), 'LMM did not grant the required application scopes.');
    }
    return {
      type: 'oauth', access, refresh, expires: startedAt + lifetime * 1000,
      lmm_issuer: this.http.issuer, lmm_resource: this.http.resource,
      lmm_session: previous?.lmm_session ?? randomUUID(), scope,
    };
  }

  /** Native production callback: no network and no storage outside Pi. */
  async refreshBlocked(): Promise<never> {
    throw new LmmError('refresh_storage_unverified', REFRESH_BLOCKED);
  }

  async refresh(value: OAuthCredential, signal: AbortSignal): Promise<LmmCredential> {
    const current = credential(value, this.http.issuer);
    signal.throwIfAborted();
    if (!this.refreshJournal) throw new LmmError('refresh_storage_unverified', REFRESH_BLOCKED);
    await this.refreshJournal.begin(this.http.issuer, current.refresh);
    signal.throwIfAborted();
    return this.exchangeRefresh(current, signal);
  }

  /** Wire-level exchange only, for tests / a future reviewed host transaction adapter. Not wired into the extension. */
  async exchangeRefresh(value: LmmCredential, signal: AbortSignal): Promise<LmmCredential> {
    const current = credential(value, this.http.issuer);
    const startedAt = Date.now();
    const response = await this.http.form('/api/oauth2/token', {
      grant_type: 'refresh_token', client_id: CLIENT_ID, refresh_token: current.refresh, resource: this.http.resource,
    }, signal);
    return this.parseToken(response, startedAt, current);
  }

  /** Access-token revocation invalidates the whole family in the fixed LMM profile. No refresh is attempted. */
  async revoke(access: string, signal: AbortSignal): Promise<void> {
    await this.http.form('/api/oauth2/revoke', {
      client_id: CLIENT_ID, token: accessToken(access), token_type_hint: 'access_token',
    }, signal);
  }
}
