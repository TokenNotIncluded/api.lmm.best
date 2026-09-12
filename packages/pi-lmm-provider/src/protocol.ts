import type { OAuthCredential } from '@earendil-works/pi-ai';

export const PROVIDER_ID = 'lmm';
export const CLIENT_ID = 'lmm-pi';
export const CALLBACK_PATH = '/oauth/lmm/callback';
export const INITIAL_SCOPES = ['catalog:read', 'balance:read', 'models:invoke'] as const;
export const SUPPORTED_APIS = ['openai-completions', 'openai-responses', 'anthropic-messages'] as const;
export type LmmApi = (typeof SUPPORTED_APIS)[number];

/** Messages are deliberately fixed: never include an HTTP body, code, or credential. */
export class LmmError extends Error {
  readonly code: string;
  constructor(code: string, message: string) {
    super(message);
    this.name = 'LmmError';
    this.code = code;
  }
}

export function requireValue(condition: unknown, message = 'Invalid LMM protocol response.'): asserts condition {
  if (!condition) throw new LmmError('invalid_response', message);
}

export function object(value: unknown): Record<string, unknown> {
  requireValue(typeof value === 'object' && value !== null && !Array.isArray(value));
  return value as Record<string, unknown>;
}

export function text(value: unknown, max = 1024): string {
  requireValue(typeof value === 'string' && value.length > 0 && value.length <= max && !/[\p{Cc}\p{Cf}]/u.test(value));
  return value;
}

export function finite(value: unknown): number {
  requireValue(typeof value === 'number' && Number.isFinite(value));
  return value;
}

export function nonnegative(value: unknown): number {
  const result = finite(value);
  requireValue(result >= 0);
  return result;
}

export function nullableNumber(value: unknown): number | null {
  return value === null ? null : nonnegative(value);
}

export function unixSeconds(value: unknown): number {
  const result = nonnegative(value);
  requireValue(Number.isSafeInteger(result));
  return result;
}

export function base64url(value: string): string {
  return Buffer.from(value, 'utf8').toString('base64url');
}

export function canonicalId(value: unknown): string {
  const id = text(value, 4096);
  requireValue(/^[A-Za-z0-9_-]+$/.test(id));
  const decoded = Buffer.from(id, 'base64url').toString('utf8');
  requireValue(base64url(decoded) === id);
  text(decoded);
  return id;
}

export function parseScope(value: unknown): string {
  const scope = text(value, 16384);
  const parts = scope.split(' ');
  requireValue(new Set(parts).size === parts.length);
  for (const part of parts) {
    if ((INITIAL_SCOPES as readonly string[]).includes(part)) continue;
    requireValue(part.startsWith('group:'));
    canonicalId(part.slice(6));
  }
  return scope;
}

export interface LmmCredential extends OAuthCredential {
  lmm_issuer: string;
  lmm_resource: string;
  lmm_session: string;
  scope: string;
}

export function accessToken(value: unknown): string {
  const token = text(value, 4096);
  requireValue(/^lmm_at_[A-Za-z0-9_-]+$/.test(token), 'LMM requires its OAuth access token, not an API key.');
  return token;
}

export function credential(value: unknown, issuer: string): LmmCredential {
  const item = object(value);
  requireValue(item.type === 'oauth' && item.lmm_issuer === issuer && item.lmm_resource === `${issuer}/api/oauth2`,
    'LMM credential belongs to another issuer or is not OAuth. Use /login.');
  accessToken(item.access);
  text(item.refresh, 4096);
  text(item.lmm_session, 128);
  parseScope(item.scope);
  nonnegative(item.expires);
  // SAFETY: every required canonical OAuth and LMM credential field is validated above.
  return item as unknown as LmmCredential;
}

export function safeMessage(error: unknown): string {
  return error instanceof LmmError ? error.message : 'LMM request failed. Retry read-only requests or sign in again; no credential was printed.';
}

export function boundedSignal(signal?: AbortSignal, timeoutMs = 15_000): AbortSignal {
  return AbortSignal.any([...(signal ? [signal] : []), AbortSignal.timeout(timeoutMs)]);
}
