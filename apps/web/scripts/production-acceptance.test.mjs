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
import assert from 'node:assert/strict'
import { chmod, mkdtemp, open, rm, symlink, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'

import {
  EVIDENCE_SCHEMA_VERSION,
  MAX_EVIDENCE_BYTES,
  frontendManifestDigest,
  readAcceptanceBaselineFile,
  readCredentialsFromEnvironment,
  runProductionAcceptance as runProductionAcceptanceRaw,
  serializeAcceptanceEvidence,
  validateAcceptanceBaseline,
} from './production-acceptance-lib.mjs'

const ROOT_PASSWORD = 'root-secret-password'
const TEST_API_KEY = 'sk-test-secret-value-123456'
const ROOT_ACCESS = 'root-access-secret-123456'
const TEST_ACCESS = 'test-access-secret-123456'
const FRONTEND_INDEX = '<!doctype html><script src="/static/app.js"></script>'
const FRONTEND_ASSET = 'globalThis.__acceptanceAsset = true'
const DEADLINE_EPOCH = Math.floor(Date.now() / 1000) + 300
const CLEANUP_DEADLINE_EPOCH = DEADLINE_EPOCH + 300
const FRONTEND_DIGEST = frontendManifestDigest([
  { path: '/', bytes: Buffer.from(FRONTEND_INDEX) },
  { path: '/static/app.js', bytes: Buffer.from(FRONTEND_ASSET) },
])
const BINDINGS = {
  deployment_id: 'deploy-42',
  backend_revision: 'revision-42',
  frontend_release: 'release-42',
  frontend_digest: FRONTEND_DIGEST,
  deadline_epoch: DEADLINE_EPOCH,
  cleanup_deadline_epoch: CLEANUP_DEADLINE_EPOCH,
}

function json(status, body, headers = {}) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json', ...headers },
  })
}

function success(data) {
  return json(200, { success: true, message: '', data })
}

function fixtureFetch({
  channelFailure = false,
  quotaGrantFailure = false,
  timeoutStage = null,
  bodyTimeoutStage = null,
  oversizedBodyStage = null,
  cleanupFailure = false,
  backendRevision = BINDINGS.backend_revision,
  frontendIndex = FRONTEND_INDEX,
  frontendAsset = FRONTEND_ASSET,
  turnstileRequired = false,
  completionUsage = { completion_tokens: 1 },
} = {}) {
  const calls = []
  let deletedToken = false
  let deletedUser = false
  let testQuota = 0
  let testUsername = ''
  let requestCount = 0
  const fetchImpl = async (url, init) => {
    requestCount += 1
    const parsed = new URL(url)
    const body = init.body ? JSON.parse(init.body) : null
    calls.push({
      origin: parsed.origin,
      path: parsed.pathname,
      search: parsed.search,
      method: init.method,
      body,
      headers: init.headers,
      signal: init.signal,
    })
    if (timeoutStage && parsed.pathname === timeoutStage) {
      await new Promise((_resolve, reject) => {
        init.signal.addEventListener(
          'abort',
          () => reject(new DOMException('aborted', 'AbortError')),
          { once: true }
        )
      })
    }
    if (bodyTimeoutStage && parsed.pathname === bodyTimeoutStage) {
      return new Response(
        new ReadableStream({
          start(controller) {
            controller.enqueue(new TextEncoder().encode('{"object":"list"'))
          },
        }),
        { status: 200, headers: { 'content-type': 'application/json' } }
      )
    }
    if (oversizedBodyStage && parsed.pathname === oversizedBodyStage) {
      return new Response(
        new ReadableStream({
          start(controller) {
            controller.enqueue(new Uint8Array(1024 * 1024 + 1))
            controller.close()
          },
        }),
        { status: 200, headers: { 'content-type': 'application/json' } }
      )
    }
    if (parsed.pathname === '/api/user/login' && init.method === 'POST') {
      if (
        turnstileRequired &&
        body.username === 'root-admin' &&
        !parsed.searchParams.get('turnstile')
      ) {
        return json(200, {
          success: false,
          code: 'turnstile_required',
          message: 'Turnstile token required',
        })
      }
      if (body.username === 'root-admin') {
        return json(
          200,
          {
            success: true,
            data: {
              access_token: ROOT_ACCESS,
              session: { sid: 'root-sid' },
              user: { id: 1, username: 'root-admin', role: 100 },
            },
          },
          { 'set-cookie': 'refresh_root=root-cookie; Path=/;' }
        )
      }
      testUsername = body.username
      return json(
        200,
        {
          success: true,
          data: {
            access_token: TEST_ACCESS,
            session: { sid: 'test-sid' },
            user: { id: 42, username: testUsername, role: 1 },
          },
        },
        { 'set-cookie': 'refresh_test=test-cookie; Path=/;' }
      )
    }
    if (parsed.pathname === '/api/status') {
      return json(200, {
        success: true,
        ready: true,
        data: { revision: backendRevision },
      })
    }
    if (parsed.pathname === '/') {
      return new Response(frontendIndex, {
        status: 200,
        headers: { 'content-type': 'text/html' },
      })
    }
    if (parsed.pathname === '/static/app.js') {
      return new Response(frontendAsset, {
        status: 200,
        headers: { 'content-type': 'text/javascript' },
      })
    }
    if (parsed.pathname === '/api/user/self') {
      return init.headers.get('Authorization') === `Bearer ${ROOT_ACCESS}`
        ? success({ id: 1, username: 'root-admin', role: 100 })
        : success({ id: 42, username: testUsername, role: 1 })
    }
    if (parsed.pathname === '/api/user/' && init.method === 'POST') {
      testUsername = body.username
      return success(null)
    }
    if (parsed.pathname === '/api/user/search') {
      return success({
        items: deletedUser
          ? []
          : [
              {
                id: 42,
                username: testUsername,
                role: 1,
                quota: testQuota,
              },
            ],
        total: deletedUser ? 0 : 1,
      })
    }
    if (parsed.pathname === '/api/user/manage' && init.method === 'POST') {
      if (quotaGrantFailure) {
        return json(200, { success: false, message: 'quota grant rejected' })
      }
      testQuota = body.value
      return success(null)
    }
    if (parsed.pathname === '/api/user/42' && init.method === 'GET') {
      return success(
        deletedUser
          ? null
          : {
              id: 42,
              username: testUsername,
              role: 1,
              quota: testQuota,
            }
      )
    }
    if (parsed.pathname === '/api/user/42' && init.method === 'DELETE') {
      deletedUser = true
      return success(null)
    }
    if (parsed.pathname === '/api/token/' && init.method === 'POST') {
      return success(null)
    }
    if (parsed.pathname === '/api/token/' && init.method === 'GET') {
      return success({
        items: deletedToken
          ? []
          : [
              {
                id: 77,
                name: `${testUsername}-acceptance`,
                key: 'sk-[masked]',
                status: 1,
              },
            ],
        total: deletedToken ? 0 : 1,
      })
    }
    if (parsed.pathname === '/api/token/77/key') {
      return success({ key: TEST_API_KEY })
    }
    if (parsed.pathname === '/api/token/77' && init.method === 'DELETE') {
      if (cleanupFailure) {
        return json(200, { success: false, message: TEST_API_KEY })
      }
      deletedToken = true
      return success(null)
    }
    if (parsed.pathname === '/api/user/models') return success(['safe-model'])
    if (parsed.pathname === '/api/usage/token/') {
      return json(200, { code: true, data: { object: 'token_usage' } })
    }
    if (parsed.pathname === '/api/channel/' && init.method === 'GET') {
      const page = Number(parsed.searchParams.get('p'))
      if (page === 1) {
        return success({
          items: [
            {
              id: 10,
              name: 'OpenAI',
              type: 1,
              status: 1,
              test_model: 'safe-model',
            },
            {
              id: 11,
              name: 'Disabled',
              type: 1,
              status: 0,
              test_model: 'safe-model',
            },
          ],
          total: 3,
          page,
          page_size: 100,
        })
      }
      return success({
        items: [
          {
            id: 12,
            name: 'Anthropic',
            type: 14,
            status: 1,
            test_model: 'safe-model',
          },
        ],
        total: 3,
        page,
        page_size: 100,
      })
    }
    if (
      parsed.pathname === '/api/channel/test/10' ||
      parsed.pathname === '/api/channel/test/12'
    ) {
      if (channelFailure && parsed.pathname.endsWith('/12')) {
        return json(200, {
          success: false,
          message: `upstream leaked ${TEST_API_KEY}`,
        })
      }
      return success(null)
    }
    if (parsed.pathname === '/v1/models') {
      return json(200, { object: 'list', data: [{ id: 'safe-model' }] })
    }
    if (parsed.pathname === '/v1/chat/completions') {
      return json(200, {
        id: 'completion',
        choices: [{ message: { content: 'ok' } }],
        usage: completionUsage,
      })
    }
    if (parsed.pathname === '/api/user/auth/logout') return success(null)
    if (parsed.pathname === '/api/user/auth/refresh') {
      return json(401, { success: false, message: ROOT_PASSWORD })
    }
    throw new Error(`unhandled ${init.method} ${parsed.pathname}`)
  }
  return {
    fetchImpl,
    calls,
    get requestCount() {
      return requestCount
    },
  }
}

function completeBaseline(overrides = {}) {
  const bindings = overrides.bindings ?? BINDINGS
  const enabledChannels = overrides.enabled_channels ?? [
    { id: 10, type: 1 },
    { id: 12, type: 14 },
  ]
  return {
    schema_version: EVIDENCE_SCHEMA_VERSION,
    mode: 'baseline',
    target: 'https://api.lmm.best',
    success: true,
    bindings,
    checks: {
      root_role: true,
      enabled_channel_count: enabledChannels.length,
      root_logout_refresh: true,
    },
    channels: [],
    enabled_channels: enabledChannels,
    failures: [],
    cleanup: {
      attempts: {
        token_delete: false,
        test_user_logout: false,
        user_delete: false,
        root_logout: true,
      },
      token_deleted: false,
      user_deleted: false,
      retained_test_identity: null,
      retained_token: null,
    },
    ...overrides,
  }
}

function runProductionAcceptance(options) {
  const mode = options.mode ?? 'verify'
  return runProductionAcceptanceRaw({
    bindings: BINDINGS,
    baseline: mode === 'verify' ? completeBaseline() : undefined,
    deadlineEpochMs: BINDINGS.deadline_epoch * 1000,
    cleanupDeadlineEpochMs: BINDINGS.cleanup_deadline_epoch * 1000,
    ...options,
  })
}

function callCount(calls, path, method) {
  return calls.filter(
    (call) => call.path === path && (!method || call.method === method)
  ).length
}

test('successful acceptance is redacted, exact, serial, and cleans up', async () => {
  const fixture = fixtureFetch()
  const summary = await runProductionAcceptance({
    credentials: {
      username: 'root-admin',
      password: ROOT_PASSWORD,
      completion_model: 'safe-model',
    },
    fetchImpl: fixture.fetchImpl,
    timeoutMs: 100,
  })
  const output = JSON.stringify(summary)
  assert.equal(summary.success, true, JSON.stringify(summary))
  assert.equal(summary.channels.length, 3)
  assert.equal(
    summary.channels
      .filter((channel) => channel.enabled)
      .every((channel) => channel.passed),
    true
  )
  assert.equal(
    summary.channels.find((channel) => channel.id === 11).passed,
    null
  )
  assert.equal(callCount(fixture.calls, '/api/channel/test/10'), 1)
  assert.equal(callCount(fixture.calls, '/api/channel/test/12'), 1)
  assert.equal(callCount(fixture.calls, '/api/channel/test/11'), 0)
  assert.equal(callCount(fixture.calls, '/api/user/', 'POST'), 1)
  assert.equal(callCount(fixture.calls, '/api/user/manage', 'POST'), 1)
  assert.equal(callCount(fixture.calls, '/api/token/', 'POST'), 1)
  assert.equal(callCount(fixture.calls, '/api/channel/', 'GET'), 2)
  assert.equal(summary.cleanup.token_deleted, true)
  assert.equal(summary.cleanup.user_deleted, true)
  assert.equal(summary.checks.funded_test_user, true)
  assert.equal(output.includes(ROOT_PASSWORD), false)
  assert.equal(output.includes(TEST_API_KEY), false)
  assert.equal(
    fixture.calls.every((call) => call.signal instanceof AbortSignal),
    true
  )
  assert.equal(
    fixture.calls.every((call) => call.origin === 'https://api.lmm.best'),
    true
  )
  const modelsCall = fixture.calls.find((call) => call.path === '/v1/models')
  const quotaCall = fixture.calls.find(
    (call) => call.path === '/api/user/manage'
  )
  const completionCall = fixture.calls.find(
    (call) => call.path === '/v1/chat/completions'
  )
  assert.equal(
    modelsCall.headers.get('Authorization'),
    `Bearer ${TEST_API_KEY}`
  )
  assert.equal(quotaCall.headers.get('Authorization'), `Bearer ${ROOT_ACCESS}`)
  assert.deepEqual(quotaCall.body, {
    id: 42,
    action: 'add_quota',
    mode: 'override',
    value: 10_000,
  })
  assert.equal(
    completionCall.headers.get('Authorization'),
    `Bearer ${TEST_API_KEY}`
  )
  assert.deepEqual(completionCall.body, {
    model: 'safe-model',
    messages: [{ role: 'user', content: 'ping' }],
    max_tokens: 1,
    stream: false,
  })
})

test('a channel failure propagates and secrets do not reach the summary', async () => {
  const fixture = fixtureFetch({ channelFailure: true })
  const summary = await runProductionAcceptance({
    credentials: {
      username: 'root-admin',
      password: ROOT_PASSWORD,
      completion_model: 'safe-model',
    },
    fetchImpl: fixture.fetchImpl,
    timeoutMs: 100,
  })
  assert.equal(summary.success, false)
  assert.equal(summary.channels.length, 3)
  assert.equal(
    summary.channels.find((channel) => channel.id === 12).passed,
    false
  )
  assert.equal(JSON.stringify(summary).includes(TEST_API_KEY), false)
  assert.equal(
    summary.failures.some((failure) => failure.stage === 'channel_validation'),
    true
  )
  assert.equal(summary.cleanup.user_deleted, true)
})

test('quota grant failure propagates after one exact attempt and user cleanup', async () => {
  const fixture = fixtureFetch({ quotaGrantFailure: true })
  const summary = await runProductionAcceptance({
    credentials: {
      username: 'root-admin',
      password: ROOT_PASSWORD,
      completion_model: 'safe-model',
    },
    fetchImpl: fixture.fetchImpl,
    timeoutMs: 100,
  })
  assert.equal(summary.success, false)
  assert.equal(callCount(fixture.calls, '/api/user/manage', 'POST'), 1)
  assert.equal(callCount(fixture.calls, '/api/token/', 'POST'), 0)
  assert.equal(
    summary.failures.some((failure) => failure.stage === 'test_user_funding'),
    true
  )
  assert.equal(summary.checks.funded_test_user, undefined)
  assert.equal(summary.cleanup.user_deleted, true)
})

test('requests are bounded and timeout failures are reported', async () => {
  const fixture = fixtureFetch({ timeoutStage: '/v1/models' })
  const started = Date.now()
  const summary = await runProductionAcceptance({
    credentials: {
      username: 'root-admin',
      password: ROOT_PASSWORD,
      completion_model: 'safe-model',
    },
    fetchImpl: fixture.fetchImpl,
    timeoutMs: 15,
  })
  assert.equal(summary.success, false)
  assert.equal(
    summary.failures.some((failure) => failure.code === 'REQUEST_TIMEOUT'),
    true
  )
  assert.ok(Date.now() - started < 500)
  assert.equal(summary.cleanup.user_deleted, true)
})

test('deadline covers a never-ending chunked response body after headers', async () => {
  const fixture = fixtureFetch({ bodyTimeoutStage: '/v1/models' })
  const started = Date.now()
  const summary = await runProductionAcceptance({
    credentials: {
      username: 'root-admin',
      password: ROOT_PASSWORD,
      completion_model: 'safe-model',
    },
    fetchImpl: fixture.fetchImpl,
    timeoutMs: 15,
  })
  assert.equal(summary.success, false)
  assert.equal(
    summary.failures.some((failure) => failure.code === 'REQUEST_TIMEOUT'),
    true
  )
  assert.ok(Date.now() - started < 500)
  assert.equal(summary.cleanup.token_deleted, true)
  assert.equal(summary.cleanup.user_deleted, true)
})

test('headerless oversized response body fails before unbounded buffering', async () => {
  const fixture = fixtureFetch({ oversizedBodyStage: '/v1/models' })
  const summary = await runProductionAcceptance({
    credentials: {
      username: 'root-admin',
      password: ROOT_PASSWORD,
      completion_model: 'safe-model',
    },
    fetchImpl: fixture.fetchImpl,
    timeoutMs: 100,
  })
  assert.equal(summary.success, false)
  assert.equal(
    summary.failures.some((failure) => failure.code === 'RESPONSE_TOO_LARGE'),
    true
  )
  assert.equal(summary.cleanup.token_deleted, true)
  assert.equal(summary.cleanup.user_deleted, true)
})

test('credential environment requires exactly one approved source', async () => {
  await assert.rejects(
    readCredentialsFromEnvironment({}),
    (error) => error.code === 'AMBIGUOUS_CREDENTIAL_SOURCE'
  )
  await assert.rejects(
    readCredentialsFromEnvironment({
      LMM_ACCEPTANCE_CREDENTIAL_FILE: '/run/acceptance.json',
      LMM_ACCEPTANCE_CREDENTIAL_FD: '3',
    }),
    (error) => error.code === 'AMBIGUOUS_CREDENTIAL_SOURCE'
  )
  await assert.rejects(
    readCredentialsFromEnvironment({ LMM_ACCEPTANCE_CREDENTIAL_FD: '2' }),
    (error) => error.code === 'INVALID_CREDENTIAL_FD'
  )
})

test('baseline is nonempty, complete, bound, and contains IDs/types only', () => {
  assert.deepEqual(validateAcceptanceBaseline(completeBaseline(), BINDINGS), {
    bindings: BINDINGS,
    enabled_channels: [
      { id: 10, type: 1 },
      { id: 12, type: 14 },
    ],
  })
  assert.throws(
    () =>
      validateAcceptanceBaseline(completeBaseline({ enabled_channels: [] })),
    (error) => error.code === 'EMPTY_BASELINE'
  )
  assert.throws(
    () => validateAcceptanceBaseline(completeBaseline({ success: false })),
    (error) => error.code === 'INCOMPLETE_BASELINE'
  )
  assert.throws(
    () =>
      validateAcceptanceBaseline(
        completeBaseline({ enabled_channels: [{ id: 10, type: 1, name: 'x' }] })
      ),
    (error) => error.code === 'INVALID_BASELINE_CHANNEL'
  )
  assert.throws(
    () =>
      validateAcceptanceBaseline(completeBaseline(), {
        ...BINDINGS,
        deployment_id: 'other-deployment',
      }),
    (error) => error.code === 'BASELINE_BINDING_MISMATCH'
  )
  assert.throws(
    () => validateAcceptanceBaseline({ ...completeBaseline(), mutable: true }),
    (error) => error.code === 'INVALID_BASELINE_SHAPE'
  )
  const partial = completeBaseline()
  delete partial.checks
  assert.throws(
    () => validateAcceptanceBaseline(partial),
    (error) => error.code === 'INVALID_BASELINE_SHAPE'
  )
  assert.throws(
    () =>
      validateAcceptanceBaseline(
        completeBaseline({
          failures: [{ stage: 'x', code: 'x', detail: 'mutable failure' }],
        })
      ),
    (error) => error.code === 'INVALID_BASELINE_FAILURES'
  )
  assert.throws(
    () =>
      validateAcceptanceBaseline(completeBaseline(), {
        ...BINDINGS,
        deadline_epoch: BINDINGS.deadline_epoch + 1,
      }),
    (error) => error.code === 'BASELINE_BINDING_MISMATCH'
  )
})

test('baseline mode emits only the enabled channel identity set', async () => {
  const fixture = fixtureFetch()
  const summary = await runProductionAcceptance({
    mode: 'baseline',
    bindings: BINDINGS,
    credentials: {
      username: 'root-admin',
      password: ROOT_PASSWORD,
      completion_model: 'safe-model',
    },
    fetchImpl: fixture.fetchImpl,
    timeoutMs: 100,
  })
  assert.equal(summary.success, true, JSON.stringify(summary))
  assert.deepEqual(summary.enabled_channels, [
    { id: 10, type: 1 },
    { id: 12, type: 14 },
  ])
  assert.equal(callCount(fixture.calls, '/api/user/', 'POST'), 0)
})

test('verify binds backend, frontend digest/assets, and exact channel set', async () => {
  const fixture = fixtureFetch()
  const summary = await runProductionAcceptance({
    mode: 'verify',
    bindings: BINDINGS,
    baseline: completeBaseline(),
    credentials: {
      username: 'root-admin',
      password: ROOT_PASSWORD,
      completion_model: 'safe-model',
    },
    fetchImpl: fixture.fetchImpl,
    timeoutMs: 100,
  })
  assert.equal(summary.success, true, JSON.stringify(summary))
  assert.equal(summary.checks.backend_identity, true)
  assert.equal(summary.checks.frontend_identity, true)
  assert.equal(summary.checks.frontend_assets, 1)
  assert.equal(summary.checks.channel_baseline, true)
  assert.equal(callCount(fixture.calls, '/static/app.js'), 1)
})

test('verify rejects channel and frontend identity mismatches', async () => {
  const channelFixture = fixtureFetch()
  const channelSummary = await runProductionAcceptance({
    mode: 'verify',
    bindings: BINDINGS,
    baseline: completeBaseline({
      enabled_channels: [{ id: 10, type: 1 }],
    }),
    credentials: {
      username: 'root-admin',
      password: ROOT_PASSWORD,
      completion_model: 'safe-model',
    },
    fetchImpl: channelFixture.fetchImpl,
    timeoutMs: 100,
  })
  assert.equal(channelSummary.success, false)
  assert.equal(
    channelSummary.failures.some(
      (failure) => failure.code === 'CHANNEL_BASELINE_MISMATCH'
    ),
    true
  )

  const frontendFixture = fixtureFetch({
    frontendIndex: '<script src="/static/app.js?v=2"></script>',
  })
  const frontendSummary = await runProductionAcceptance({
    mode: 'verify',
    bindings: BINDINGS,
    baseline: completeBaseline(),
    credentials: {
      username: 'root-admin',
      password: ROOT_PASSWORD,
      completion_model: 'safe-model',
    },
    fetchImpl: frontendFixture.fetchImpl,
    timeoutMs: 100,
  })
  assert.equal(frontendSummary.success, false)
  assert.equal(
    frontendSummary.failures.some(
      (failure) => failure.code === 'FRONTEND_DIGEST_MISMATCH'
    ),
    true
  )
})

test('absolute global deadline expires strictly before cleanup window', async () => {
  const fixture = fixtureFetch({ timeoutStage: '/v1/models' })
  const deadlineEpoch = Math.ceil(Date.now() / 1000) + 1
  const cleanupDeadlineEpoch = deadlineEpoch + 5
  const bindings = {
    ...BINDINGS,
    deadline_epoch: deadlineEpoch,
    cleanup_deadline_epoch: cleanupDeadlineEpoch,
  }
  const summary = await runProductionAcceptance({
    bindings,
    baseline: completeBaseline({ bindings }),
    credentials: {
      username: 'root-admin',
      password: ROOT_PASSWORD,
      completion_model: 'safe-model',
    },
    fetchImpl: fixture.fetchImpl,
    timeoutMs: 10_000,
    deadlineEpochMs: deadlineEpoch * 1000,
    cleanupDeadlineEpochMs: cleanupDeadlineEpoch * 1000,
  })
  assert.equal(summary.success, false)
  assert.equal(
    summary.failures.some(
      (failure) => failure.code === 'GLOBAL_DEADLINE_EXCEEDED'
    ),
    true
  )
  assert.equal(callCount(fixture.calls, '/api/token/77', 'DELETE'), 1)
  assert.equal(callCount(fixture.calls, '/api/user/42', 'DELETE'), 1)
  assert.equal(callCount(fixture.calls, '/api/user/auth/logout', 'POST'), 2)
  assert.deepEqual(summary.cleanup.attempts, {
    token_delete: true,
    test_user_logout: true,
    user_delete: true,
    root_logout: true,
  })
  assert.equal(summary.cleanup.token_deleted, true)
  assert.equal(summary.cleanup.user_deleted, true)

  const unsafeDeadline = Math.floor(Date.now() / 1000) + 10
  const unsafeBindings = {
    ...BINDINGS,
    deadline_epoch: unsafeDeadline,
    cleanup_deadline_epoch: unsafeDeadline + 1,
  }
  await assert.rejects(
    runProductionAcceptanceRaw({
      bindings: unsafeBindings,
      baseline: completeBaseline({ bindings: unsafeBindings }),
      credentials: {
        username: 'root-admin',
        password: ROOT_PASSWORD,
        completion_model: 'safe-model',
      },
      fetchImpl: fixture.fetchImpl,
      deadlineEpochMs: unsafeDeadline * 1000,
      cleanupDeadlineEpochMs: (unsafeDeadline + 1) * 1000,
    }),
    (error) => error.code === 'UNSAFE_CLEANUP_DEADLINE'
  )
})

test('login omits empty Turnstile query and reports a required challenge', async () => {
  const normalFixture = fixtureFetch()
  const normal = await runProductionAcceptance({
    mode: 'baseline',
    credentials: {
      username: 'root-admin',
      password: ROOT_PASSWORD,
      completion_model: 'safe-model',
    },
    fetchImpl: normalFixture.fetchImpl,
    timeoutMs: 100,
  })
  assert.equal(normal.success, true)
  assert.equal(
    normalFixture.calls.find((call) => call.path === '/api/user/login').search,
    ''
  )

  const requiredFixture = fixtureFetch({ turnstileRequired: true })
  const required = await runProductionAcceptance({
    mode: 'baseline',
    credentials: {
      username: 'root-admin',
      password: ROOT_PASSWORD,
      completion_model: 'safe-model',
    },
    fetchImpl: requiredFixture.fetchImpl,
    timeoutMs: 100,
  })
  assert.equal(required.success, false)
  assert.equal(
    required.failures.some((failure) => failure.code === 'TURNSTILE_REQUIRED'),
    true
  )

  const tokenFixture = fixtureFetch({ turnstileRequired: true })
  const token = await runProductionAcceptance({
    mode: 'baseline',
    credentials: {
      username: 'root-admin',
      password: ROOT_PASSWORD,
      turnstile_token: 'turnstile-secret-token',
      completion_model: 'safe-model',
    },
    fetchImpl: tokenFixture.fetchImpl,
    timeoutMs: 100,
  })
  assert.equal(token.success, true, JSON.stringify(token))
  assert.equal(
    tokenFixture.calls.find((call) => call.path === '/api/user/login').search,
    '?turnstile=turnstile-secret-token'
  )
  assert.equal(JSON.stringify(token).includes('turnstile-secret-token'), false)
})

test('completion requires bounded response usage evidence', async () => {
  for (const completionUsage of [null, {}, { completion_tokens: 2 }]) {
    const fixture = fixtureFetch({ completionUsage })
    const summary = await runProductionAcceptance({
      credentials: {
        username: 'root-admin',
        password: ROOT_PASSWORD,
        completion_model: 'safe-model',
      },
      fetchImpl: fixture.fetchImpl,
      timeoutMs: 100,
    })
    assert.equal(summary.success, false)
    assert.equal(
      summary.failures.some(
        (failure) => failure.code === 'INVALID_COMPLETION_USAGE'
      ),
      true
    )
    assert.equal(summary.cleanup.token_deleted, true)
    assert.equal(summary.cleanup.user_deleted, true)
  }
})

test('frontend manifest rejects duplicate and cross-origin references', async () => {
  for (const frontendIndex of [
    '<script src="/static/app.js"></script><script src="/static/app.js"></script>',
    '<script src="https://cdn.example.invalid/app.js"></script>',
  ]) {
    const fixture = fixtureFetch({ frontendIndex })
    const summary = await runProductionAcceptance({
      credentials: {
        username: 'root-admin',
        password: ROOT_PASSWORD,
        completion_model: 'safe-model',
      },
      fetchImpl: fixture.fetchImpl,
      timeoutMs: 100,
    })
    assert.equal(summary.success, false)
    assert.equal(summary.cleanup.attempts.root_logout, true)
    assert.equal(callCount(fixture.calls, '/api/user/', 'POST'), 0)
  }
})

test('secure baseline and credential inputs reject unsafe files and descriptors', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'lmm-acceptance-input.'))
  try {
    const baselinePath = join(directory, 'baseline.json')
    const credentialPath = join(directory, 'credentials.json')
    const linkPath = join(directory, 'baseline-link.json')
    await writeFile(baselinePath, JSON.stringify(completeBaseline()), {
      mode: 0o600,
    })
    await writeFile(
      credentialPath,
      JSON.stringify({
        username: 'root-admin',
        password: ROOT_PASSWORD,
        completion_model: 'safe-model',
      }),
      { mode: 0o600 }
    )
    await symlink(baselinePath, linkPath)
    await assert.rejects(readAcceptanceBaselineFile('relative.json'))
    await assert.rejects(readAcceptanceBaselineFile(linkPath))

    await chmod(baselinePath, 0o644)
    await assert.rejects(
      readAcceptanceBaselineFile(baselinePath),
      (error) => error.code === 'UNSAFE_INPUT'
    )
    await chmod(baselinePath, 0o600)

    const credentialHandle = await open(credentialPath, 'r')
    try {
      if (process.getuid?.() === 0) {
        assert.deepEqual(
          await readCredentialsFromEnvironment({
            LMM_ACCEPTANCE_CREDENTIAL_FD: String(credentialHandle.fd),
          }),
          {
            username: 'root-admin',
            password: ROOT_PASSWORD,
            completion_model: 'safe-model',
          }
        )
        assert.deepEqual(
          validateAcceptanceBaseline(
            await readAcceptanceBaselineFile(baselinePath),
            BINDINGS
          ).bindings,
          BINDINGS
        )
      } else {
        await assert.rejects(
          readCredentialsFromEnvironment({
            LMM_ACCEPTANCE_CREDENTIAL_FD: String(credentialHandle.fd),
          }),
          (error) => error.code === 'UNSAFE_INPUT'
        )
      }
    } finally {
      await credentialHandle.close()
    }

    await writeFile(baselinePath, 'x'.repeat(MAX_EVIDENCE_BYTES + 1))
    await chmod(baselinePath, 0o600)
    await assert.rejects(
      readAcceptanceBaselineFile(baselinePath),
      (error) => error.code === 'UNSAFE_INPUT'
    )
  } finally {
    await rm(directory, { recursive: true, force: true })
  }
})

test('cleanup failures are retained as redacted evidence without skipping user cleanup', async () => {
  const fixture = fixtureFetch({ cleanupFailure: true })
  const summary = await runProductionAcceptance({
    credentials: {
      username: 'root-admin',
      password: ROOT_PASSWORD,
      completion_model: 'safe-model',
    },
    fetchImpl: fixture.fetchImpl,
    timeoutMs: 100,
  })
  const output = serializeAcceptanceEvidence(summary)
  assert.equal(summary.success, false)
  assert.equal(summary.cleanup.user_deleted, true)
  assert.equal(summary.cleanup.token_deleted, false)
  assert.equal(summary.cleanup.retained_token.id, 77)
  assert.equal(output.includes(TEST_API_KEY), false)
  assert.ok(Buffer.byteLength(output) <= MAX_EVIDENCE_BYTES)
})

test('evidence serialization rejects unbounded output', () => {
  assert.throws(
    () =>
      serializeAcceptanceEvidence({ detail: 'x'.repeat(MAX_EVIDENCE_BYTES) }),
    (error) => error.code === 'EVIDENCE_TOO_LARGE'
  )
})
