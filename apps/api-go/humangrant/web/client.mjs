// HumanGrant 0.1 strict profile. No human cookie, dashboard token or private key
// from another principal is used. Callers retain their own CryptoKey in memory.
const enc = new TextEncoder()
export const b64 = bytes => btoa(String.fromCharCode(...new Uint8Array(bytes)))
export const b64url = bytes => b64(bytes).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '')
const hash = bytes => crypto.subtle.digest('SHA-256', bytes)
export async function newActor() {
  const keys = await crypto.subtle.generateKey({ name: 'Ed25519' }, false, ['sign', 'verify'])
  const raw = await crypto.subtle.exportKey('raw', keys.publicKey)
  return { privateKey: keys.privateKey, publicKey: b64url(raw), id: 'ed25519:' + b64url(await hash(raw)) }
}
export async function signHeaders(actor, origin, method, path, body = '', token = 'anonymous', options = {}) {
  const url = new URL(origin)
  if (url.origin !== origin || !path.startsWith('/') || /[%?#\\]/.test(path) || path.includes('//')) throw new Error('Invalid target')
  const created = options.created ?? Math.floor(Date.now() / 1000)
  const nonce = options.nonce ?? b64url(crypto.getRandomValues(new Uint8Array(24)))
  const digest = 'sha-256=:' + b64(await hash(enc.encode(body))) + ':'
  const authorization = 'HumanGrant ' + token
  const params = '("@method" "@target-uri" "content-digest" "content-type" "x-hg-key" "authorization");created=' + created + ';expires=' + (created + 60) + ';nonce="' + nonce + '";keyid="' + actor.id + '";alg="ed25519";tag="humangrant-0.1"'
  const base = ['"@method": ' + method, '"@target-uri": ' + origin + path, '"content-digest": ' + digest, '"content-type": application/json', '"x-hg-key": ' + actor.publicKey, '"authorization": ' + authorization, '"@signature-params": ' + params].join('\n')
  const signature = await crypto.subtle.sign('Ed25519', actor.privateKey, enc.encode(base))
  return { 'Content-Type': 'application/json', 'Content-Digest': digest, 'X-HG-Key': actor.publicKey, Authorization: authorization, 'Signature-Input': 'hg=' + params, Signature: 'hg=:' + b64(signature) + ':' }
}
export class HGError extends Error {
  constructor(status, code, retry = 0) { super(code); this.status = status; this.retry = retry }
}
export async function signedFetch(actor, origin, method, path, value, token = 'anonymous') {
  const body = value === undefined ? '' : JSON.stringify(value)
  const headers = await signHeaders(actor, origin, method, path, body, token)
  const response = await fetch(origin + path, { method, headers, body: method === 'GET' ? undefined : body, credentials: 'omit', redirect: 'error', cache: 'no-store', referrerPolicy: 'no-referrer', signal: AbortSignal.timeout(15000) })
  if (!response.ok) {
    const data = await response.json().catch(() => ({}))
    throw new HGError(response.status, data.error ?? 'request_failed', Number(response.headers.get('Retry-After')) || 0)
  }
  if (path.endsWith('/status.md')) {
    if (!(response.headers.get('Content-Type') ?? '').startsWith('text/markdown')) throw new Error('Not Markdown')
    return response.text()
  }
  return response.json()
}
export function parseState(markdown) {
  const data = Object.create(null)
  for (const line of markdown.split('\n')) {
    const match = /^([a-z_]+): (.+)$/.exec(line)
    if (!match) continue
    if (Object.hasOwn(data, match[1])) throw new Error('Duplicate state field')
    data[match[1]] = JSON.parse(match[2])
  }
  if (data.protocol_version !== 'HumanGrant Protocol 0.1 Draft' || !Number.isSafeInteger(data.expires_at) || !Number.isInteger(data.poll_after_seconds) || data.poll_after_seconds < 0 || data.poll_after_seconds > 60) throw new Error('Invalid state')
  return data
}
