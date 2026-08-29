#!/usr/bin/env node
/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { createHash, randomBytes } from 'node:crypto'
import { constants as fsConstants, fstat, read as fsRead } from 'node:fs'
import { open } from 'node:fs/promises'
import { promisify } from 'node:util'

export const PRODUCTION_ORIGIN = 'https://api.lmm.best'

const ROOT_ROLE = 100
const COMMON_ROLE = 1
const ENABLED = 1
const TEST_USER_QUOTA = 10_000
const MAX_RESPONSE_BYTES = 1024 * 1024
const MAX_PAGES = 1000
const MAX_FRONTEND_ASSETS = 128
const MAX_INPUT_BYTES = 64 * 1024
const CLEANUP_DEADLINE_MARGIN_MS = 1000
export const EVIDENCE_SCHEMA_VERSION = 3
export const MAX_EVIDENCE_BYTES = 64 * 1024

const fstatAsync = promisify(fstat)
const readAsync = promisify(fsRead)

class AcceptanceError extends Error {
  constructor(stage, code, message, options = {}) {
    super(message, options)
    this.name = 'AcceptanceError'
    this.stage = stage
    this.code = code
  }
}

export class SecretRedactor {
  #secrets = new Set()

  add(value) {
    if (typeof value === 'string' && value.length >= 3) this.#secrets.add(value)
  }

  text(value) {
    let result = String(value ?? '')
    for (const secret of [...this.#secrets].sort(
      (a, b) => b.length - a.length
    )) {
      result = result.replaceAll(secret, '[REDACTED]')
    }
    result = result
      .replaceAll(/\bsk-[A-Za-z0-9._-]{8,}\b/g, '[REDACTED_API_KEY]')
      .replaceAll(/\bBearer\s+[^\s,;]+/gi, 'Bearer [REDACTED]')
      .replaceAll(/[\r\n\t]/g, ' ')
      .replaceAll('\0', ' ')
    return result.slice(0, 300)
  }
}

function fail(stage, code, message, options) {
  throw new AcceptanceError(stage, code, message, options)
}

function requestFailure(aborted, globalDeadlineWins, networkDetail) {
  if (!aborted) return { code: 'NETWORK_ERROR', detail: networkDetail }
  if (globalDeadlineWins) {
    return {
      code: 'GLOBAL_DEADLINE_EXCEEDED',
      detail: 'acceptance global deadline expired',
    }
  }
  return { code: 'REQUEST_TIMEOUT', detail: 'request timed out' }
}

function assertObject(value, stage, code) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    fail(stage, code, 'response is not an object')
  }
  return value
}

function assertApiSuccess(value, stage) {
  const body = assertObject(value, stage, 'INVALID_API_RESPONSE')
  if (body.success !== true) {
    fail(stage, 'API_REJECTED', 'API returned success=false')
  }
  return body
}

function cookiePair(setCookie) {
  if (typeof setCookie !== 'string' || setCookie.length === 0) return null
  const pair = setCookie.split(';', 1)[0]
  return /^[!#$%&'*+.^_`|~0-9A-Za-z-]+=[^;]*$/.test(pair) ? pair : null
}

function extractSetCookies(headers) {
  if (typeof headers.getSetCookie === 'function') return headers.getSetCookie()
  const combined = headers.get('set-cookie')
  return combined ? [combined] : []
}

function randomIdentifier() {
  const stamp = Date.now().toString(36).slice(-7)
  const suffix = randomBytes(3).toString('hex')
  return `lmmacc_${stamp}_${suffix}`.slice(0, 20)
}

function randomPassword() {
  return randomBytes(14).toString('base64url').slice(0, 20)
}

function parseJson(raw, stage, code) {
  let value
  try {
    value = JSON.parse(raw)
  } catch (error) {
    fail(stage, code, `${stage} is not valid JSON`, { cause: error })
  }
  return value
}

function normalizeCredentials(raw) {
  const value = parseJson(raw, 'credentials', 'INVALID_CREDENTIAL_JSON')
  assertObject(value, 'credentials', 'INVALID_CREDENTIALS')
  const allowed = new Set([
    'username',
    'password',
    'totp_code',
    'turnstile_token',
    'completion_model',
  ])
  for (const key of Object.keys(value)) {
    if (!allowed.has(key)) {
      fail(
        'credentials',
        'UNKNOWN_CREDENTIAL_FIELD',
        'credentials contain an unknown field'
      )
    }
  }
  if (typeof value.username !== 'string' || value.username.length === 0) {
    fail('credentials', 'MISSING_USERNAME', 'credentials require username')
  }
  if (typeof value.password !== 'string' || value.password.length === 0) {
    fail('credentials', 'MISSING_PASSWORD', 'credentials require password')
  }
  for (const key of ['totp_code', 'turnstile_token', 'completion_model']) {
    if (value[key] !== undefined && typeof value[key] !== 'string') {
      fail('credentials', 'INVALID_CREDENTIAL_FIELD', `${key} must be a string`)
    }
  }
  if (
    typeof value.completion_model !== 'string' ||
    value.completion_model.length === 0
  ) {
    fail(
      'credentials',
      'MISSING_COMPLETION_MODEL',
      'credentials require an explicit completion_model'
    )
  }
  return value
}

function requireSecureInputStat(stat, stage) {
  if (
    !stat.isFile() ||
    stat.uid !== 0 ||
    (stat.mode & 0o777) !== 0o600 ||
    stat.size > MAX_INPUT_BYTES
  ) {
    fail(
      stage,
      'UNSAFE_INPUT',
      `${stage} input must be a root-owned mode-0600 regular file no larger than 64 KiB`
    )
  }
}

async function readBoundedDescriptor(fd, stage) {
  const chunks = []
  let total = 0
  while (total <= MAX_INPUT_BYTES) {
    const buffer = Buffer.alloc(Math.min(8192, MAX_INPUT_BYTES + 1 - total))
    const { bytesRead } = await readAsync(fd, buffer, 0, buffer.length, null)
    if (bytesRead === 0) return Buffer.concat(chunks, total).toString('utf8')
    chunks.push(buffer.subarray(0, bytesRead))
    total += bytesRead
  }
  fail(stage, 'INPUT_TOO_LARGE', `${stage} input exceeded 64 KiB`)
}

async function readSecureFile(path, stage) {
  if (typeof path !== 'string' || !path.startsWith('/')) {
    fail(stage, 'RELATIVE_INPUT_FILE', `${stage} file path must be absolute`)
  }
  let handle
  try {
    handle = await open(
      path,
      fsConstants.O_RDONLY | (fsConstants.O_NOFOLLOW ?? 0)
    )
    requireSecureInputStat(await handle.stat(), stage)
    return await readBoundedDescriptor(handle.fd, stage)
  } finally {
    await handle?.close()
  }
}

export async function readCredentialsFromEnvironment(env = process.env) {
  const credentialFile = env.LMM_ACCEPTANCE_CREDENTIAL_FILE
  const credentialFd = env.LMM_ACCEPTANCE_CREDENTIAL_FD
  if (Boolean(credentialFile) === Boolean(credentialFd)) {
    fail(
      'credentials',
      'AMBIGUOUS_CREDENTIAL_SOURCE',
      'set exactly one credential source'
    )
  }

  if (credentialFd) {
    if (!/^[0-9]+$/.test(credentialFd) || Number(credentialFd) <= 2) {
      fail(
        'credentials',
        'INVALID_CREDENTIAL_FD',
        'credential FD must be an inherited descriptor above 2'
      )
    }
    const fd = Number(credentialFd)
    requireSecureInputStat(await fstatAsync(fd), 'credentials')
    return normalizeCredentials(await readBoundedDescriptor(fd, 'credentials'))
  }
  return normalizeCredentials(
    await readSecureFile(credentialFile, 'credentials')
  )
}

export async function readAcceptanceBaselineFile(path) {
  return parseJson(
    await readSecureFile(path, 'baseline'),
    'baseline',
    'INVALID_BASELINE_JSON'
  )
}

class HttpSession {
  constructor({ fetchImpl, timeoutMs, deadlineAt, redactor }) {
    this.fetchImpl = fetchImpl
    this.timeoutMs = timeoutMs
    this.deadlineAt = deadlineAt
    this.redactor = redactor
    this.accessToken = null
    this.sid = null
    this.cookies = new Map()
  }

  #cookieHeader() {
    return [...this.cookies.values()].join('; ')
  }

  #captureCookies(headers) {
    for (const header of extractSetCookies(headers)) {
      const pair = cookiePair(header)
      if (!pair) continue
      const separator = pair.indexOf('=')
      const name = pair.slice(0, separator)
      this.cookies.set(name, pair)
      this.redactor.add(pair.slice(separator + 1))
    }
  }

  withDeadline(deadlineAt, timeoutMs = this.timeoutMs) {
    const session = new HttpSession({
      fetchImpl: this.fetchImpl,
      timeoutMs,
      deadlineAt,
      redactor: this.redactor,
    })
    session.accessToken = this.accessToken
    session.sid = this.sid
    session.cookies = new Map(this.cookies)
    return session
  }

  async request(pathname, options = {}) {
    const url = new URL(pathname, PRODUCTION_ORIGIN)
    if (url.origin !== PRODUCTION_ORIGIN) {
      fail(
        options.stage ?? 'http',
        'INVALID_ORIGIN',
        'request escaped production origin'
      )
    }
    const remainingMs = this.deadlineAt - Date.now()
    if (remainingMs <= 0) {
      fail(
        options.stage ?? 'http',
        'GLOBAL_DEADLINE_EXCEEDED',
        'acceptance global deadline expired'
      )
    }
    const controller = new AbortController()
    const requestedTimeout = options.timeoutMs ?? this.timeoutMs
    const timeoutMs = Math.min(requestedTimeout, remainingMs)
    const globalDeadlineWins = remainingMs <= requestedTimeout
    const timer = setTimeout(() => controller.abort(), timeoutMs)
    const headers = new Headers(options.headers)
    headers.set('Accept', 'application/json')
    if (options.body !== undefined) {
      headers.set('Content-Type', 'application/json')
    }
    if (this.accessToken && options.auth !== false) {
      headers.set('Authorization', `Bearer ${this.accessToken}`)
    }
    const cookie = this.#cookieHeader()
    if (cookie) headers.set('Cookie', cookie)
    if (options.originGuard) headers.set('Origin', PRODUCTION_ORIGIN)

    try {
      let response
      try {
        response = await this.fetchImpl(url, {
          method: options.method ?? 'GET',
          headers,
          body:
            options.body === undefined
              ? undefined
              : JSON.stringify(options.body),
          redirect: 'error',
          signal: controller.signal,
        })
      } catch (error) {
        const { code, detail } = requestFailure(
          controller.signal.aborted,
          globalDeadlineWins,
          'network request failed'
        )
        fail(options.stage ?? 'http', code, detail, {
          cause: error,
        })
      }
      this.#captureCookies(response.headers)
      const contentLength = Number(response.headers.get('content-length') ?? 0)
      if (contentLength > MAX_RESPONSE_BYTES) {
        controller.abort()
        fail(
          options.stage ?? 'http',
          'RESPONSE_TOO_LARGE',
          'response exceeded size limit'
        )
      }

      let text = ''
      let responseBytes = new Uint8Array()
      if (response.body) {
        const reader = response.body.getReader()
        const chunks = []
        let totalBytes = 0
        const cancelReader = () => {
          void reader.cancel().catch(() => {})
        }
        controller.signal.addEventListener('abort', cancelReader, {
          once: true,
        })
        try {
          while (true) {
            const { done, value } = await reader.read()
            if (controller.signal.aborted) {
              const code = globalDeadlineWins
                ? 'GLOBAL_DEADLINE_EXCEEDED'
                : 'REQUEST_TIMEOUT'
              fail(
                options.stage ?? 'http',
                code,
                code === 'GLOBAL_DEADLINE_EXCEEDED'
                  ? 'acceptance global deadline expired'
                  : 'request timed out'
              )
            }
            if (done) break
            if (!(value instanceof Uint8Array)) {
              fail(
                options.stage ?? 'http',
                'INVALID_RESPONSE_BODY',
                'response body contained an invalid chunk'
              )
            }
            totalBytes += value.byteLength
            if (totalBytes > MAX_RESPONSE_BYTES) {
              controller.abort()
              fail(
                options.stage ?? 'http',
                'RESPONSE_TOO_LARGE',
                'response exceeded size limit'
              )
            }
            chunks.push(value)
          }
        } catch (error) {
          if (error instanceof AcceptanceError) throw error
          const { code, detail } = requestFailure(
            controller.signal.aborted,
            globalDeadlineWins,
            'response body read failed'
          )
          fail(options.stage ?? 'http', code, detail, { cause: error })
        } finally {
          controller.signal.removeEventListener('abort', cancelReader)
          reader.releaseLock()
        }
        const bytes = new Uint8Array(totalBytes)
        let offset = 0
        for (const chunk of chunks) {
          bytes.set(chunk, offset)
          offset += chunk.byteLength
        }
        responseBytes = bytes
        text = new TextDecoder().decode(bytes)
      }

      let body = null
      if (options.responseType !== 'bytes' && text.length > 0) {
        try {
          body = JSON.parse(text)
        } catch (error) {
          fail(
            options.stage ?? 'http',
            'INVALID_JSON',
            'response was not JSON',
            { cause: error }
          )
        }
      }
      if (!response.ok && !options.allowHttpFailure) {
        fail(
          options.stage ?? 'http',
          `HTTP_${response.status}`,
          'HTTP request failed'
        )
      }
      return {
        status: response.status,
        ok: response.ok,
        body,
        bytes: options.responseType === 'bytes' ? responseBytes : undefined,
      }
    } finally {
      clearTimeout(timer)
    }
  }
}

function loginRequiresTurnstile(body) {
  if (!body || typeof body !== 'object') return false
  if (
    body.turnstile_required === true ||
    body.data?.turnstile_required === true ||
    body.data?.require_turnstile === true
  ) {
    return true
  }
  const marker = `${body.code ?? ''} ${body.message ?? ''}`
  return /turnstile|captcha/i.test(marker)
}

async function login(session, credentials, redactor, stage) {
  let loginPath = '/api/user/login'
  if (credentials.turnstile_token) {
    redactor.add(credentials.turnstile_token)
    loginPath += `?turnstile=${encodeURIComponent(credentials.turnstile_token)}`
  }
  const first = await session.request(loginPath, {
    method: 'POST',
    body: { username: credentials.username, password: credentials.password },
    auth: false,
    stage,
  })
  if (!credentials.turnstile_token && loginRequiresTurnstile(first.body)) {
    fail(
      stage,
      'TURNSTILE_REQUIRED',
      'login requires a turnstile_token credential'
    )
  }
  let loginBody = assertApiSuccess(first.body, stage)
  if (loginBody.data?.require_2fa === true) {
    if (
      !credentials.totp_code ||
      typeof loginBody.data.flow_token !== 'string'
    ) {
      fail(stage, 'TWO_FACTOR_REQUIRED', 'login requires a current 2FA code')
    }
    redactor.add(credentials.totp_code)
    const second = await session.request('/api/user/login/2fa', {
      method: 'POST',
      body: {
        code: credentials.totp_code,
        flow_token: loginBody.data.flow_token,
      },
      auth: false,
      stage,
    })
    loginBody = assertApiSuccess(second.body, stage)
  }
  const bundle = assertObject(loginBody.data, stage, 'INVALID_AUTH_BUNDLE')
  if (
    typeof bundle.access_token !== 'string' ||
    bundle.access_token.length < 8
  ) {
    fail(stage, 'MISSING_ACCESS_TOKEN', 'login did not return an access token')
  }
  if (!bundle.session || typeof bundle.session.sid !== 'string') {
    fail(stage, 'MISSING_SESSION_ID', 'login did not return a session ID')
  }
  session.accessToken = bundle.access_token
  session.sid = bundle.session.sid
  redactor.add(bundle.access_token)
  redactor.add(bundle.session.sid)
  return bundle.user
}

async function requireSelf(session, expected, stage) {
  const response = await session.request('/api/user/self', { stage })
  const body = assertApiSuccess(response.body, stage)
  const user = assertObject(body.data, stage, 'INVALID_SELF_RESPONSE')
  if (expected.role !== undefined && user.role !== expected.role) {
    fail(stage, 'ROLE_MISMATCH', 'authenticated user has the wrong role')
  }
  if (expected.username !== undefined && user.username !== expected.username) {
    fail(
      stage,
      'IDENTITY_MISMATCH',
      'authenticated user identity does not match'
    )
  }
  return user
}

async function findExactUser(root, username) {
  const query = new URLSearchParams({
    keyword: username,
    p: '1',
    page_size: '100',
  })
  const response = await root.request(`/api/user/search?${query}`, {
    stage: 'test_user_lookup',
  })
  const body = assertApiSuccess(response.body, 'test_user_lookup')
  const items = body.data?.items
  if (!Array.isArray(items)) {
    fail(
      'test_user_lookup',
      'INVALID_USER_LIST',
      'user search did not return items'
    )
  }
  return items.filter((item) => item?.username === username)
}

async function listTokens(session) {
  const tokens = []
  for (let page = 1; page <= MAX_PAGES; page += 1) {
    const response = await session.request(`/api/token/?p=${page}&size=100`, {
      stage: 'token_list',
    })
    const body = assertApiSuccess(response.body, 'token_list')
    const items = body.data?.items
    if (!Array.isArray(items)) {
      fail(
        'token_list',
        'INVALID_TOKEN_LIST',
        'token list did not return items'
      )
    }
    tokens.push(...items)
    const total = Number(body.data?.total)
    if (!Number.isSafeInteger(total) || total < 0) {
      fail('token_list', 'INVALID_TOKEN_TOTAL', 'token total is invalid')
    }
    if (tokens.length >= total) return tokens
    if (items.length === 0) {
      fail(
        'token_list',
        'INCOMPLETE_TOKEN_LIST',
        'token pagination ended early'
      )
    }
  }
  fail(
    'token_list',
    'TOKEN_PAGE_LIMIT',
    'token pagination exceeded safety limit'
  )
}

async function listChannels(root) {
  const channels = []
  const seen = new Set()
  for (let page = 1; page <= MAX_PAGES; page += 1) {
    const response = await root.request(
      `/api/channel/?p=${page}&page_size=100`,
      {
        stage: 'channel_list',
      }
    )
    const body = assertApiSuccess(response.body, 'channel_list')
    const items = body.data?.items
    if (!Array.isArray(items)) {
      fail(
        'channel_list',
        'INVALID_CHANNEL_LIST',
        'channel list did not return items'
      )
    }
    for (const channel of items) {
      if (
        !Number.isSafeInteger(channel?.id) ||
        !Number.isSafeInteger(channel?.type) ||
        typeof channel?.name !== 'string' ||
        seen.has(channel.id)
      ) {
        fail(
          'channel_list',
          'INVALID_CHANNEL_ID',
          'channel list contains an invalid or duplicate ID'
        )
      }
      seen.add(channel.id)
      channels.push({
        id: channel.id,
        name: channel.name,
        type: channel.type,
        status: channel.status,
        testModel: channel.test_model,
      })
    }
    const total = Number(body.data?.total)
    if (!Number.isSafeInteger(total) || total < 0) {
      fail('channel_list', 'INVALID_CHANNEL_TOTAL', 'channel total is invalid')
    }
    if (channels.length >= total) return channels
    if (items.length === 0) {
      fail(
        'channel_list',
        'INCOMPLETE_CHANNEL_LIST',
        'channel pagination ended early'
      )
    }
  }
  fail(
    'channel_list',
    'CHANNEL_PAGE_LIMIT',
    'channel pagination exceeded safety limit'
  )
}

function publicFailure(error, redactor) {
  if (error instanceof AcceptanceError) {
    return {
      stage: error.stage,
      code: error.code,
      detail: redactor.text(error.message),
    }
  }
  return {
    stage: 'internal',
    code: 'UNEXPECTED_ERROR',
    detail: redactor.text(error?.message ?? 'unexpected error'),
  }
}

async function logoutAndRejectRefresh(session, stage) {
  const logout = await session.request('/api/user/auth/logout', {
    method: 'POST',
    headers: session.sid ? { 'X-Auth-Session': session.sid } : undefined,
    originGuard: true,
    stage,
  })
  assertApiSuccess(logout.body, stage)
  session.accessToken = null
  const refresh = await session.request('/api/user/auth/refresh', {
    method: 'POST',
    originGuard: true,
    auth: false,
    allowHttpFailure: true,
    stage,
  })
  if (refresh.ok && refresh.body?.success === true) {
    fail(stage, 'REFRESH_SURVIVED_LOGOUT', 'refresh succeeded after logout')
  }
}

function chooseCompletionModel(models, requested) {
  const ids = models
    .map((entry) => (typeof entry === 'string' ? entry : entry?.id))
    .filter((id) => typeof id === 'string' && id.length > 0)
  if (typeof requested !== 'string' || requested.length === 0) {
    fail(
      'api_key_models',
      'MISSING_COMPLETION_MODEL',
      'an explicit completion model is required'
    )
  }
  if (!ids.includes(requested)) {
    fail(
      'api_key_models',
      'REQUESTED_MODEL_UNAVAILABLE',
      'configured completion model is unavailable'
    )
  }
  return requested
}

function normalizeBindings(bindings) {
  const value = assertObject(bindings, 'bindings', 'MISSING_BINDINGS')
  const expectedKeys = [
    'backend_revision',
    'cleanup_deadline_epoch',
    'deadline_epoch',
    'deployment_id',
    'frontend_digest',
    'frontend_release',
  ]
  if (Object.keys(value).sort().join(',') !== expectedKeys.join(',')) {
    fail(
      'bindings',
      'INVALID_BINDING_SHAPE',
      'bindings must contain only the exact deployment identity and deadlines'
    )
  }
  const deploymentId = value.deployment_id
  const backendRevision = value.backend_revision
  const frontendRelease = value.frontend_release
  const frontendDigest = value.frontend_digest
  for (const [name, candidate] of [
    ['deployment_id', deploymentId],
    ['backend_revision', backendRevision],
    ['frontend_release', frontendRelease],
  ]) {
    if (
      typeof candidate !== 'string' ||
      !/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(candidate)
    ) {
      fail('bindings', 'INVALID_BINDING', `${name} is invalid`)
    }
  }
  if (
    typeof frontendDigest !== 'string' ||
    !/^[a-f0-9]{64}$/.test(frontendDigest)
  ) {
    fail(
      'bindings',
      'INVALID_FRONTEND_DIGEST',
      'frontend_digest must be a lowercase SHA-256 digest'
    )
  }
  if (
    !Number.isSafeInteger(value.deadline_epoch) ||
    !Number.isSafeInteger(value.cleanup_deadline_epoch) ||
    value.deadline_epoch <= 0 ||
    value.deadline_epoch >= value.cleanup_deadline_epoch
  ) {
    fail(
      'bindings',
      'INVALID_DEADLINE_BINDING',
      'deadline bindings must be positive integer epochs ordered before the cleanup window'
    )
  }
  return {
    deployment_id: deploymentId,
    backend_revision: backendRevision,
    frontend_release: frontendRelease,
    frontend_digest: frontendDigest,
    deadline_epoch: value.deadline_epoch,
    cleanup_deadline_epoch: value.cleanup_deadline_epoch,
  }
}

function enabledChannelIdentities(channels) {
  return channels
    .filter((channel) => channel.status === ENABLED)
    .map(({ id, type }) => ({ id, type }))
    .sort((left, right) => left.id - right.id || left.type - right.type)
}

export function validateAcceptanceBaseline(value, expectedBindings) {
  const baseline = assertObject(value, 'baseline', 'INVALID_BASELINE')
  const baselineKeys = [
    'bindings',
    'channels',
    'checks',
    'cleanup',
    'enabled_channels',
    'failures',
    'mode',
    'schema_version',
    'success',
    'target',
  ]
  if (Object.keys(baseline).sort().join(',') !== baselineKeys.join(',')) {
    fail(
      'baseline',
      'INVALID_BASELINE_SHAPE',
      'baseline contains missing or unexpected fields'
    )
  }
  if (baseline.schema_version !== EVIDENCE_SCHEMA_VERSION) {
    fail(
      'baseline',
      'BASELINE_SCHEMA_MISMATCH',
      'baseline schema is unsupported'
    )
  }
  if (baseline.mode !== 'baseline' || baseline.success !== true) {
    fail(
      'baseline',
      'INCOMPLETE_BASELINE',
      'baseline is not complete and successful'
    )
  }
  if (baseline.target !== PRODUCTION_ORIGIN) {
    fail('baseline', 'BASELINE_TARGET_MISMATCH', 'baseline target is invalid')
  }
  if (!Array.isArray(baseline.channels) || baseline.channels.length !== 0) {
    fail(
      'baseline',
      'INVALID_BASELINE_CHANNELS',
      'baseline channels must be empty'
    )
  }
  if (!Array.isArray(baseline.failures) || baseline.failures.length !== 0) {
    fail(
      'baseline',
      'INVALID_BASELINE_FAILURES',
      'baseline failures must be empty'
    )
  }
  const bindings = normalizeBindings(baseline.bindings)
  if (
    expectedBindings &&
    JSON.stringify(bindings) !==
      JSON.stringify(normalizeBindings(expectedBindings))
  ) {
    fail(
      'baseline',
      'BASELINE_BINDING_MISMATCH',
      'baseline identity bindings differ'
    )
  }
  if (
    !Array.isArray(baseline.enabled_channels) ||
    baseline.enabled_channels.length === 0
  ) {
    fail('baseline', 'EMPTY_BASELINE', 'baseline requires enabled channels')
  }
  const identities = []
  const seen = new Set()
  for (const channel of baseline.enabled_channels) {
    if (
      !channel ||
      typeof channel !== 'object' ||
      Array.isArray(channel) ||
      Object.keys(channel).sort().join(',') !== 'id,type' ||
      !Number.isSafeInteger(channel.id) ||
      !Number.isSafeInteger(channel.type) ||
      seen.has(channel.id)
    ) {
      fail(
        'baseline',
        'INVALID_BASELINE_CHANNEL',
        'baseline channel identities must contain unique integer IDs and types only'
      )
    }
    seen.add(channel.id)
    identities.push({ id: channel.id, type: channel.type })
  }
  identities.sort((left, right) => left.id - right.id || left.type - right.type)
  const checks = assertObject(
    baseline.checks,
    'baseline',
    'INVALID_BASELINE_CHECKS'
  )
  if (
    Object.keys(checks).sort().join(',') !==
      'enabled_channel_count,root_logout_refresh,root_role' ||
    checks.root_role !== true ||
    checks.root_logout_refresh !== true ||
    checks.enabled_channel_count !== identities.length
  ) {
    fail(
      'baseline',
      'INVALID_BASELINE_CHECKS',
      'baseline checks are incomplete or inconsistent'
    )
  }
  const cleanup = assertObject(
    baseline.cleanup,
    'baseline',
    'INVALID_BASELINE_CLEANUP'
  )
  if (
    Object.keys(cleanup).sort().join(',') !==
      'attempts,retained_test_identity,retained_token,token_deleted,user_deleted' ||
    cleanup.token_deleted !== false ||
    cleanup.user_deleted !== false ||
    cleanup.retained_test_identity !== null ||
    cleanup.retained_token !== null
  ) {
    fail(
      'baseline',
      'INVALID_BASELINE_CLEANUP',
      'baseline cleanup evidence is invalid'
    )
  }
  const attempts = assertObject(
    cleanup.attempts,
    'baseline',
    'INVALID_BASELINE_CLEANUP'
  )
  if (
    Object.keys(attempts).sort().join(',') !==
      'root_logout,test_user_logout,token_delete,user_delete' ||
    attempts.root_logout !== true ||
    attempts.test_user_logout !== false ||
    attempts.token_delete !== false ||
    attempts.user_delete !== false
  ) {
    fail(
      'baseline',
      'INVALID_BASELINE_CLEANUP',
      'baseline cleanup attempts are invalid'
    )
  }
  return { bindings, enabled_channels: identities }
}

function requireExactBaseline(actual, baseline) {
  if (JSON.stringify(actual) !== JSON.stringify(baseline.enabled_channels)) {
    fail(
      'channel_baseline',
      'CHANNEL_BASELINE_MISMATCH',
      'enabled channel identity set differs from the baseline'
    )
  }
}

async function verifyBackendIdentity(session, revision) {
  const response = await session.request('/api/status', {
    auth: false,
    stage: 'backend_identity',
  })
  const body = assertApiSuccess(response.body, 'backend_identity')
  if (body.ready !== true) {
    fail('backend_identity', 'BACKEND_NOT_READY', 'backend status is not ready')
  }
  if (body.data?.revision !== revision && body.data?.version !== revision) {
    fail(
      'backend_identity',
      'BACKEND_REVISION_MISMATCH',
      'backend revision does not match the deployment binding'
    )
  }
}

function frontendAssetPaths(indexText) {
  const assets = []
  const seen = new Set()
  for (const match of indexText.matchAll(/(?:src|href)=["']([^"']+)["']/gi)) {
    const raw = match[1]
    if (!raw || raw.startsWith('data:') || raw.startsWith('#')) continue
    const url = new URL(raw, PRODUCTION_ORIGIN)
    if (url.origin !== PRODUCTION_ORIGIN) {
      fail(
        'frontend_assets',
        'CROSS_ORIGIN_FRONTEND_ASSET',
        'frontend index references a cross-origin asset'
      )
    }
    const path = `${url.pathname}${url.search}`
    if (url.pathname === '/' || seen.has(path)) {
      fail(
        'frontend_assets',
        'DUPLICATE_FRONTEND_ASSET',
        'frontend index references a duplicate or invalid asset path'
      )
    }
    seen.add(path)
    assets.push(path)
  }
  return assets.sort()
}

export function frontendManifestDigest(entries) {
  const normalized = [...entries]
    .map(({ path, bytes }) => ({
      path,
      sha256: createHash('sha256').update(bytes).digest('hex'),
    }))
    .sort((left, right) => left.path.localeCompare(right.path))
  return createHash('sha256').update(JSON.stringify(normalized)).digest('hex')
}

async function verifyFrontendIdentity(session, bindings) {
  const index = await session.request('/', {
    auth: false,
    responseType: 'bytes',
    stage: 'frontend_identity',
  })
  const paths = frontendAssetPaths(new TextDecoder().decode(index.bytes))
  if (paths.length === 0 || paths.length > MAX_FRONTEND_ASSETS) {
    fail(
      'frontend_assets',
      'INVALID_FRONTEND_ASSET_SET',
      'frontend index must reference a bounded nonempty asset set'
    )
  }
  const entries = [{ path: '/', bytes: index.bytes }]
  for (const path of paths) {
    const asset = await session.request(path, {
      auth: false,
      responseType: 'bytes',
      stage: 'frontend_assets',
    })
    entries.push({ path, bytes: asset.bytes })
  }
  if (frontendManifestDigest(entries) !== bindings.frontend_digest) {
    fail(
      'frontend_identity',
      'FRONTEND_DIGEST_MISMATCH',
      'frontend manifest digest does not match the deployment binding'
    )
  }
  return paths.length
}

export function serializeAcceptanceEvidence(value) {
  const output = `${JSON.stringify(value)}\n`
  if (Buffer.byteLength(output) > MAX_EVIDENCE_BYTES) {
    fail(
      'evidence',
      'EVIDENCE_TOO_LARGE',
      'acceptance evidence exceeded its size bound'
    )
  }
  return output
}

export async function runProductionAcceptance({
  credentials,
  fetchImpl = globalThis.fetch,
  timeoutMs = 20_000,
  mode = 'verify',
  bindings: rawBindings,
  baseline: rawBaseline,
  deadlineEpochMs,
  cleanupDeadlineEpochMs,
} = {}) {
  if (!credentials) {
    fail('credentials', 'MISSING_CREDENTIALS', 'credentials are required')
  }
  if (typeof fetchImpl !== 'function') {
    fail('startup', 'MISSING_FETCH', 'fetch implementation is unavailable')
  }
  if (
    !Number.isSafeInteger(timeoutMs) ||
    timeoutMs < 1 ||
    timeoutMs > 120_000
  ) {
    fail(
      'startup',
      'INVALID_TIMEOUT',
      'timeout must be between 1 and 120000 milliseconds'
    )
  }
  if (mode !== 'baseline' && mode !== 'verify') {
    fail('startup', 'INVALID_MODE', 'mode must be baseline or verify')
  }
  if (!rawBindings) {
    fail(
      'bindings',
      'MISSING_BINDINGS',
      'deployment identity bindings are required'
    )
  }
  const bindings = normalizeBindings(rawBindings)
  const baseline = rawBaseline
    ? validateAcceptanceBaseline(rawBaseline, bindings)
    : null
  if (mode === 'verify' && !baseline) {
    fail('baseline', 'MISSING_BASELINE', 'verify mode requires a baseline')
  }
  const now = Date.now()
  const deadlineAt = deadlineEpochMs
  const cleanupDeadlineBoundAt = cleanupDeadlineEpochMs
  if (
    deadlineAt !== bindings.deadline_epoch * 1000 ||
    cleanupDeadlineBoundAt !== bindings.cleanup_deadline_epoch * 1000
  ) {
    fail(
      'bindings',
      'DEADLINE_BINDING_MISMATCH',
      'runtime deadlines do not match the evidence bindings'
    )
  }
  if (!Number.isSafeInteger(deadlineAt) || deadlineAt <= now) {
    fail(
      'startup',
      'INVALID_GLOBAL_DEADLINE',
      'global deadline must be in the future'
    )
  }
  const cleanupDeadlineAt = cleanupDeadlineBoundAt - CLEANUP_DEADLINE_MARGIN_MS
  if (
    !Number.isSafeInteger(cleanupDeadlineBoundAt) ||
    deadlineAt + 1000 > cleanupDeadlineAt
  ) {
    fail(
      'startup',
      'UNSAFE_CLEANUP_DEADLINE',
      'global deadline must reserve bounded cleanup time before cleanup deadline'
    )
  }
  const cleanupTimeoutMs = Math.max(
    1,
    Math.min(timeoutMs, Math.floor((cleanupDeadlineAt - deadlineAt) / 16))
  )
  const redactor = new SecretRedactor()
  redactor.add(credentials.username)
  redactor.add(credentials.password)
  redactor.add(credentials.totp_code)
  redactor.add(credentials.turnstile_token)
  const sessionOptions = { fetchImpl, timeoutMs, deadlineAt, redactor }
  const root = new HttpSession(sessionOptions)
  const testUserSession = new HttpSession(sessionOptions)
  const username = randomIdentifier()
  const password = randomPassword()
  const tokenName = `${username}-acceptance`
  redactor.add(password)

  const summary = {
    schema_version: EVIDENCE_SCHEMA_VERSION,
    mode,
    target: PRODUCTION_ORIGIN,
    bindings,
    success: false,
    checks: {},
    channels: [],
    failures: [],
    cleanup: {
      attempts: {
        token_delete: false,
        test_user_logout: false,
        user_delete: false,
        root_logout: false,
      },
      token_deleted: false,
      user_deleted: false,
      retained_test_identity: null,
      retained_token: null,
    },
  }
  let createdUser = null
  let createdToken = null
  let userCreateAttempted = false
  let tokenCreateAttempted = false
  let rootLoggedIn = false
  let testUserLoggedIn = false

  const recordFailure = (error) =>
    summary.failures.push(publicFailure(error, redactor))

  try {
    const loginUser = await login(root, credentials, redactor, 'root_login')
    rootLoggedIn = true
    if (loginUser?.role !== ROOT_ROLE) {
      fail('root_login', 'ROLE_MISMATCH', 'login bundle is not a root user')
    }
    const rootSelf = await requireSelf(
      root,
      { role: ROOT_ROLE, username: credentials.username },
      'root_role'
    )
    summary.checks.root_role = rootSelf.role === ROOT_ROLE

    if (mode === 'baseline') {
      const identities = enabledChannelIdentities(await listChannels(root))
      if (identities.length === 0) {
        fail('baseline', 'EMPTY_BASELINE', 'no enabled channels were found')
      }
      summary.enabled_channels = identities
      summary.checks.enabled_channel_count = identities.length
      summary.cleanup.attempts.root_logout = true
      await logoutAndRejectRefresh(root, 'root_logout_refresh')
      rootLoggedIn = false
      summary.checks.root_logout_refresh = true
      summary.success = true
      return summary
    }

    if (bindings) {
      const publicSession = new HttpSession(sessionOptions)
      await verifyBackendIdentity(publicSession, bindings.backend_revision)
      summary.checks.backend_identity = true
      summary.checks.frontend_assets = await verifyFrontendIdentity(
        publicSession,
        bindings
      )
      summary.checks.frontend_identity = true
    }

    userCreateAttempted = true
    const createUser = await root.request('/api/user/', {
      method: 'POST',
      body: { username, display_name: username, password, role: COMMON_ROLE },
      stage: 'test_user_create',
    })
    assertApiSuccess(createUser.body, 'test_user_create')
    const exactUsers = await findExactUser(root, username)
    if (exactUsers.length !== 1 || !Number.isSafeInteger(exactUsers[0].id)) {
      fail(
        'test_user_lookup',
        'NON_UNIQUE_TEST_USER',
        'test user lookup did not return exactly one identity'
      )
    }
    createdUser = { id: exactUsers[0].id, username }
    summary.checks.test_user_created = true

    const fundUser = await root.request('/api/user/manage', {
      method: 'POST',
      body: {
        id: createdUser.id,
        action: 'add_quota',
        mode: 'override',
        value: TEST_USER_QUOTA,
      },
      stage: 'test_user_funding',
    })
    assertApiSuccess(fundUser.body, 'test_user_funding')
    const fundedLookup = await root.request(`/api/user/${createdUser.id}`, {
      stage: 'test_user_funding',
    })
    const fundedUser = assertApiSuccess(
      fundedLookup.body,
      'test_user_funding'
    ).data
    if (
      fundedUser?.id !== createdUser.id ||
      fundedUser?.username !== createdUser.username ||
      fundedUser?.quota !== TEST_USER_QUOTA
    ) {
      fail(
        'test_user_funding',
        'TEST_USER_QUOTA_MISMATCH',
        'test user quota verification failed'
      )
    }
    summary.checks.funded_test_user = true

    await login(
      testUserSession,
      { username, password },
      redactor,
      'test_user_login'
    )
    testUserLoggedIn = true
    const testSelf = await requireSelf(
      testUserSession,
      { role: COMMON_ROLE, username },
      'test_user_identity'
    )
    if (testSelf.id !== createdUser.id) {
      fail(
        'test_user_identity',
        'IDENTITY_MISMATCH',
        'test user ID does not match created identity'
      )
    }
    summary.checks.test_user_login = true

    tokenCreateAttempted = true
    const createToken = await testUserSession.request('/api/token/', {
      method: 'POST',
      body: {
        name: tokenName,
        remain_quota: 0,
        expired_time: -1,
        unlimited_quota: true,
        model_limits_enabled: false,
        model_limits: '',
        allow_ips: '',
        group: 'default',
        cross_group_retry: false,
      },
      stage: 'token_create',
    })
    assertApiSuccess(createToken.body, 'token_create')
    const matchingTokens = (await listTokens(testUserSession)).filter(
      (token) => token?.name === tokenName
    )
    if (
      matchingTokens.length !== 1 ||
      !Number.isSafeInteger(matchingTokens[0].id)
    ) {
      fail(
        'token_list',
        'NON_UNIQUE_TEST_TOKEN',
        'token list did not return exactly one created token'
      )
    }
    createdToken = { id: matchingTokens[0].id, name: tokenName }
    const reveal = await testUserSession.request(
      `/api/token/${createdToken.id}/key`,
      {
        method: 'POST',
        stage: 'token_reveal',
      }
    )
    const revealed = assertApiSuccess(reveal.body, 'token_reveal').data?.key
    if (typeof revealed !== 'string' || revealed.length < 8) {
      fail(
        'token_reveal',
        'INVALID_API_KEY',
        'token reveal did not return a usable key'
      )
    }
    redactor.add(revealed)
    summary.checks.token_created_listed_revealed = true

    await requireSelf(
      testUserSession,
      { role: COMMON_ROLE, username },
      'authenticated_api'
    )
    assertApiSuccess(
      (
        await testUserSession.request('/api/user/models', {
          stage: 'authenticated_api',
        })
      ).body,
      'authenticated_api'
    )
    const usageBody = assertObject(
      (
        await testUserSession.request('/api/usage/token/', {
          headers: { Authorization: `Bearer ${revealed}` },
          auth: false,
          stage: 'authenticated_api',
        })
      ).body,
      'authenticated_api',
      'INVALID_USAGE_RESPONSE'
    )
    if (usageBody.code !== true) {
      fail(
        'authenticated_api',
        'API_REJECTED',
        'token usage API rejected the created key'
      )
    }
    summary.checks.representative_authenticated_apis = true

    const channels = await listChannels(root)
    if (baseline) {
      requireExactBaseline(enabledChannelIdentities(channels), baseline)
      summary.checks.channel_baseline = true
    }
    summary.checks.channel_count = channels.length
    for (const channel of channels) {
      const publicChannel = {
        id: channel.id,
        type: channel.type,
        enabled: channel.status === ENABLED,
        passed: null,
      }
      if (channel.status !== ENABLED) {
        summary.channels.push(publicChannel)
        continue
      }
      publicChannel.passed = false
      try {
        const query = new URLSearchParams()
        if (typeof channel.testModel === 'string' && channel.testModel.trim()) {
          query.set('model', channel.testModel.trim())
        }
        const suffix = query.size > 0 ? `?${query}` : ''
        const validation = await root.request(
          `/api/channel/test/${channel.id}${suffix}`,
          {
            stage: 'channel_validation',
          }
        )
        assertApiSuccess(validation.body, 'channel_validation')
        publicChannel.passed = true
      } catch (error) {
        publicChannel.failure_code =
          error instanceof AcceptanceError ? error.code : 'UNEXPECTED_ERROR'
        recordFailure(error)
      }
      summary.channels.push(publicChannel)
    }
    summary.checks.enabled_channels_tested = summary.channels.filter(
      (channel) => channel.enabled
    ).length
    summary.checks.enabled_channels_passed = summary.channels.filter(
      (channel) => channel.passed
    ).length

    const apiKeySession = new HttpSession(sessionOptions)
    apiKeySession.accessToken = revealed
    const modelsResponse = await apiKeySession.request('/v1/models', {
      stage: 'api_key_models',
    })
    const modelBody = assertObject(
      modelsResponse.body,
      'api_key_models',
      'INVALID_MODELS_RESPONSE'
    )
    if (!Array.isArray(modelBody.data) || modelBody.data.length === 0) {
      fail(
        'api_key_models',
        'EMPTY_MODELS',
        'OpenAI-compatible models endpoint returned no models'
      )
    }
    const completionModel = chooseCompletionModel(
      modelBody.data,
      credentials.completion_model
    )
    summary.checks.api_key_models = true
    const completion = await apiKeySession.request('/v1/chat/completions', {
      method: 'POST',
      body: {
        model: completionModel,
        messages: [{ role: 'user', content: 'ping' }],
        max_tokens: 1,
        stream: false,
      },
      stage: 'api_key_completion',
    })
    const completionBody = assertObject(
      completion.body,
      'api_key_completion',
      'INVALID_COMPLETION_RESPONSE'
    )
    if (
      !Array.isArray(completionBody.choices) ||
      completionBody.choices.length === 0
    ) {
      fail(
        'api_key_completion',
        'EMPTY_COMPLETION',
        'completion response contained no choices'
      )
    }
    const completionTokens = completionBody.usage?.completion_tokens
    if (
      !Number.isSafeInteger(completionTokens) ||
      completionTokens < 0 ||
      completionTokens > 1
    ) {
      fail(
        'api_key_completion',
        'INVALID_COMPLETION_USAGE',
        'completion usage must prove at most one generated token'
      )
    }
    summary.checks.api_key_completion = true
  } catch (error) {
    recordFailure(error)
  } finally {
    const cleanupWindowMs = Math.floor((cleanupDeadlineAt - deadlineAt) / 4)
    const tokenCleanupSession = testUserSession.withDeadline(
      deadlineAt + cleanupWindowMs,
      cleanupTimeoutMs
    )
    const testLogoutSession = testUserSession.withDeadline(
      deadlineAt + cleanupWindowMs * 2,
      cleanupTimeoutMs
    )
    const userCleanupRoot = root.withDeadline(
      deadlineAt + cleanupWindowMs * 3,
      cleanupTimeoutMs
    )
    const rootLogoutSession = root.withDeadline(
      cleanupDeadlineAt,
      cleanupTimeoutMs
    )
    if (tokenCreateAttempted && testUserLoggedIn) {
      summary.cleanup.attempts.token_delete = true
      try {
        if (!createdToken) {
          const recoverableTokens = (
            await listTokens(tokenCleanupSession)
          ).filter((token) => token?.name === tokenName)
          if (
            recoverableTokens.length !== 1 ||
            !Number.isSafeInteger(recoverableTokens[0].id)
          ) {
            fail(
              'token_cleanup',
              'TOKEN_ID_UNAVAILABLE',
              'refused token cleanup without one exact token identity'
            )
          }
          createdToken = { id: recoverableTokens[0].id, name: tokenName }
        }
        const deletion = await tokenCleanupSession.request(
          `/api/token/${createdToken.id}`,
          {
            method: 'DELETE',
            stage: 'token_cleanup',
          }
        )
        assertApiSuccess(deletion.body, 'token_cleanup')
        const remaining = (await listTokens(tokenCleanupSession)).filter(
          (token) =>
            token?.id === createdToken.id || token?.name === createdToken.name
        )
        if (remaining.length !== 0) {
          fail(
            'token_cleanup',
            'TOKEN_RETAINED',
            'test token remained after deletion'
          )
        }
        summary.cleanup.token_deleted = true
      } catch (error) {
        summary.cleanup.retained_token = createdToken
          ? { id: createdToken.id, name: createdToken.name }
          : { id: null, name: tokenName }
        recordFailure(error)
      }
    }
    if (testUserLoggedIn) {
      summary.cleanup.attempts.test_user_logout = true
      try {
        await logoutAndRejectRefresh(
          testLogoutSession,
          'test_user_logout_refresh'
        )
        summary.checks.test_user_logout_refresh = true
      } catch (error) {
        recordFailure(error)
      }
    }
    if (userCreateAttempted && rootLoggedIn) {
      summary.cleanup.attempts.user_delete = true
      try {
        if (!createdUser) {
          const recoverableUsers = await findExactUser(
            userCleanupRoot,
            username
          )
          if (
            recoverableUsers.length !== 1 ||
            !Number.isSafeInteger(recoverableUsers[0].id)
          ) {
            fail(
              'test_user_cleanup',
              'USER_ID_UNAVAILABLE',
              'refused user cleanup without one exact user identity'
            )
          }
          createdUser = { id: recoverableUsers[0].id, username }
        }
        const lookup = await userCleanupRoot.request(
          `/api/user/${createdUser.id}`,
          { stage: 'test_user_cleanup' }
        )
        const user = assertApiSuccess(lookup.body, 'test_user_cleanup').data
        if (
          user?.id !== createdUser.id ||
          user?.username !== createdUser.username
        ) {
          fail(
            'test_user_cleanup',
            'CLEANUP_IDENTITY_MISMATCH',
            'refused to delete an unverified user identity'
          )
        }
        const deletion = await userCleanupRoot.request(
          `/api/user/${createdUser.id}`,
          {
            method: 'DELETE',
            stage: 'test_user_cleanup',
          }
        )
        assertApiSuccess(deletion.body, 'test_user_cleanup')
        if (
          (await findExactUser(userCleanupRoot, createdUser.username))
            .length !== 0
        ) {
          fail(
            'test_user_cleanup',
            'USER_RETAINED',
            'test user remained after deletion'
          )
        }
        summary.cleanup.user_deleted = true
      } catch (error) {
        summary.cleanup.retained_test_identity = createdUser ?? {
          id: null,
          username,
        }
        recordFailure(error)
      }
    }
    if (rootLoggedIn) {
      summary.cleanup.attempts.root_logout = true
      try {
        await logoutAndRejectRefresh(rootLogoutSession, 'root_logout_refresh')
        summary.checks.root_logout_refresh = true
      } catch (error) {
        recordFailure(error)
      }
    }
  }

  summary.success = summary.failures.length === 0
  return summary
}
