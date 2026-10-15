/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'

import { AxiosError, type InternalAxiosRequestConfig } from 'axios'

import { api } from '@/lib/api'
import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

import {
  commitHoneypotUsers,
  getHoneypotOperation,
  getHoneypotUserOperation,
  HoneypotFailure,
  previewHoneypotUsers,
  saveHoneypotPolicies,
  userResultMatchesPreview,
} from './api'
import {
  DEFAULT_LIMITS,
  hashHoneypotConfig,
  type HoneypotPreview,
  type HoneypotResult,
} from './contract'

const adapter = api.defaults.adapter
const previousAuth = useAuthStore.getState().auth
const scope = { userId: 901, sessionId: 'synthetic-root-session' }
function installRoot(id = 901, sid = scope.sessionId) {
  const bundle: AuthBundle = {
    access_token: 'private-synthetic-unit-token',
    token_type: 'Bearer',
    access_expires_at: 2000000000,
    user: { id, username: 'synthetic-root', role: 100 },
    session: {
      sid,
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'unit-only',
      created_at: 1,
      last_active_at: 1,
      expires_at: 2000000000,
    },
  }
  useAuthStore.getState().auth.setBundle(bundle)
}
const response = (config: InternalAxiosRequestConfig, data: unknown) => ({
  status: 200,
  statusText: 'OK',
  headers: {},
  config,
  data,
})
const preview: HoneypotPreview = {
  version: 2,
  preview_id: 'synthetic-preview',
  preview_hash: 'a'.repeat(64),
  expires_at: 2000000000,
  operation_id: 'synthetic-operation',
  action: 'mark',
  policy_id: 'fixed',
  policy_revision: 4,
  targets: [
    {
      user_id: '7',
      username: 'example',
      active: true,
      expected_revision: 2,
      before: { marked: false, policy_id: null },
      after: { marked: true, policy_id: 'fixed', active: true },
    },
  ],
}
const result: HoneypotResult = {
  version: 2,
  status: 'applied',
  operation_id: preview.operation_id,
  kind: 'users',
  policy_revision: 4,
  users: [
    {
      user_id: '7',
      username: 'example',
      email: 'example@example.test',
      active: true,
      platform_role: 'user',
      revision: 3,
      marked: true,
      policy_id: 'fixed',
    },
  ],
}
afterEach(() => {
  api.defaults.adapter = adapter
  useAuthStore.setState({ auth: previousAuth })
})

test('a commit sends only the reviewed binding and verifies the complete post-state', async () => {
  installRoot()
  const calls: InternalAxiosRequestConfig[] = []
  api.defaults.adapter = async (config) => {
    calls.push(config)
    return response(config, result)
  }
  assert.deepEqual(await commitHoneypotUsers(preview, scope), result)
  assert.equal(calls.length, 1)
  assert.equal(calls[0].url, '/core/v2/admin/abuse/users/commit')
  assert.deepEqual(JSON.parse(calls[0].data), {
    operation_id: preview.operation_id,
    preview_id: preview.preview_id,
    preview_hash: preview.preview_hash,
  })
  assert.deepEqual(calls[0].authScope, scope)
  assert.equal(
    userResultMatchesPreview(
      { ...result, users: [{ ...result.users[0], revision: 2 }] },
      preview
    ),
    false
  )
  assert.equal(
    userResultMatchesPreview(
      { ...result, users: [{ ...result.users[0], active: false }] },
      preview
    ),
    false
  )
  assert.equal(
    userResultMatchesPreview(
      { ...result, users: [{ ...result.users[0], user_id: '8' }] },
      preview
    ),
    false
  )
})
test('network and malformed successful commit outcomes are unknown, without automatic retries', async () => {
  installRoot()
  for (const transport of ['timeout', 'malformed', 'wrong-state']) {
    let count = 0
    api.defaults.adapter = async (config) => {
      count++
      if (transport === 'timeout') {
        throw new AxiosError('timeout', 'ETIMEDOUT', config)
      }
      return response(
        config,
        transport === 'malformed' ? { success: true } : { ...result, users: [] }
      )
    }
    await assert.rejects(
      commitHoneypotUsers(preview, scope),
      (error) =>
        error instanceof HoneypotFailure &&
        error.kind === 'unknown' &&
        error.operationID === preview.operation_id
    )
    assert.equal(count, 1)
  }
})
test('conflict and expired previews are refused, not reported as applied', async () => {
  installRoot()
  for (const [status, kind] of [
    [409, 'conflict'],
    [410, 'expired'],
  ] as const) {
    let count = 0
    api.defaults.adapter = async (config) => {
      count++
      throw new AxiosError(
        'private server text',
        'ERR_BAD_REQUEST',
        config,
        undefined,
        {
          ...response(config, {
            error: {
              code:
                status === 409
                  ? 'ADMIN_ABUSE_CONFLICT'
                  : 'ADMIN_ABUSE_PREVIEW_EXPIRED',
            },
          }),
          status,
        }
      )
    }
    await assert.rejects(
      commitHoneypotUsers(preview, scope),
      (error) =>
        error instanceof HoneypotFailure &&
        error.kind === kind &&
        !error.message.includes('private server text')
    )
    assert.equal(count, 1)
  }
})
test('preview mismatches cannot reach a commit', async () => {
  installRoot()
  let count = 0
  api.defaults.adapter = async (config) => {
    count++
    return response(config, {
      ...preview,
      targets: [{ ...preview.targets[0], user_id: '999' }],
    })
  }
  await assert.rejects(
    previewHoneypotUsers(
      {
        operation_id: preview.operation_id,
        reason: 'Reviewed',
        action: 'mark',
        policy_id: 'fixed',
        expected_policy_revision: 4,
        targets: [{ user_id: '7', expected_revision: 2 }],
      },
      scope
    ),
    (error) => error instanceof HoneypotFailure && error.kind === 'invalid'
  )
  assert.equal(count, 1)
})
test('policy receipts require the exact operation, new revision and empty user mutation set', async () => {
  installRoot()
  api.defaults.adapter = async (config) =>
    response(config, {
      ...result,
      version: 1,
      kind: 'policies',
      policy_revision: 5,
      users: [],
      applied_config_hash: await hashHoneypotConfig(body.config),
    })
  const body = {
    operation_id: preview.operation_id,
    expected_revision: 4,
    reason: 'Reviewed',
    config: {
      version: 1 as const,
      policies: [
        {
          id: 'fixed',
          name: 'Fixed',
          mode: 'fixed_text' as const,
          public_models: ['public'],
          fixed_text: 'Example',
          clients: {},
          limits: DEFAULT_LIMITS,
        },
      ],
    },
  }
  assert.equal((await saveHoneypotPolicies(body, scope)).policy_revision, 5)
  api.defaults.adapter = async (config) =>
    response(config, {
      ...result,
      version: 1,
      kind: 'policies',
      policy_revision: 5,
      users: result.users,
      applied_config_hash: await hashHoneypotConfig(body.config),
    })
  await assert.rejects(
    saveHoneypotPolicies(body, scope),
    (error) => error instanceof HoneypotFailure && error.kind === 'unknown'
  )
})
test('unknown recovery reads only the same operation and a new session cannot dispatch the old write', async () => {
  installRoot()
  const calls: InternalAxiosRequestConfig[] = []
  api.defaults.adapter = async (config) => {
    calls.push(config)
    return response(config, result)
  }
  assert.deepEqual(
    await getHoneypotUserOperation(preview.operation_id, scope),
    result
  )
  assert.equal(calls[0].method, 'get')
  assert.equal(
    calls[0].url,
    `/core/v2/admin/abuse/operations/${preview.operation_id}`
  )
  installRoot(902, 'other-session')
  await assert.rejects(
    commitHoneypotUsers(preview, scope),
    (error) => error instanceof HoneypotFailure
  )
  assert.equal(
    calls.length,
    1,
    'no old-account commit may dispatch after the session changes'
  )
})

test('policy receipt hashes are required, and valid mismatches are conflicts', async () => {
  installRoot()
  const body = {
    operation_id: 'policy-hash-operation',
    expected_revision: 4,
    reason: 'Reviewed',
    config: { version: 1 as const, policies: [] },
  }
  for (const [hash, expectedKind] of [
    [undefined, 'unknown'],
    ['bad-hash', 'unknown'],
    ['0'.repeat(64), 'conflict'],
  ] as const) {
    let writes = 0
    api.defaults.adapter = async (config) => {
      writes++
      return response(config, {
        version: 1,
        status: 'applied',
        kind: 'policies',
        operation_id: body.operation_id,
        policy_revision: 5,
        users: [],
        ...(hash === undefined ? {} : { applied_config_hash: hash }),
      })
    }
    await assert.rejects(
      saveHoneypotPolicies(body, scope),
      (error) => error instanceof HoneypotFailure && error.kind === expectedKind
    )
    assert.equal(writes, 1)
  }
  api.defaults.adapter = async (config) =>
    response(config, {
      version: 1,
      status: 'applied',
      kind: 'policies',
      operation_id: body.operation_id,
      policy_revision: 5,
      users: [],
    })
  await assert.rejects(
    getHoneypotOperation(body.operation_id, scope),
    (error) => error instanceof HoneypotFailure && error.kind === 'unknown'
  )
})

test('a dispatched write recognizes only the exact Core rejection code/status pair', async () => {
  installRoot()
  const cases = [
    [400, 'ADMIN_ABUSE_INVALID', 'invalid'],
    [401, 'ADMIN_ABUSE_UNAUTHORIZED', 'forbidden'],
    [403, 'ADMIN_ABUSE_FORBIDDEN', 'forbidden'],
    [404, 'ADMIN_ABUSE_NOT_FOUND', 'not_found'],
    [409, 'ADMIN_ABUSE_CONFLICT', 'conflict'],
    [410, 'ADMIN_ABUSE_PREVIEW_EXPIRED', 'expired'],
    [429, 'ADMIN_ABUSE_RATE_LIMITED', 'invalid'],
    [503, 'ADMIN_ABUSE_UNAVAILABLE', 'unknown'],
    [404, 'ADMIN_ABUSE_OPERATION_NOT_FOUND', 'unknown'],
    [403, 'ADMIN_ABUSE_CONFLICT', 'unknown'],
    [409, 'ADMIN_ABUSE_FORBIDDEN', 'unknown'],
    [410, 'ADMIN_ABUSE_CONFLICT', 'unknown'],
  ] as const
  let writes = 0
  for (const [status, code, kind] of cases) {
    api.defaults.adapter = async (config) => {
      writes++
      throw new AxiosError(
        'synthetic private text',
        'ERR_BAD_RESPONSE',
        config,
        undefined,
        { ...response(config, { error: { code } }), status }
      )
    }
    await assert.rejects(
      commitHoneypotUsers(preview, scope),
      (error) =>
        error instanceof HoneypotFailure &&
        error.kind === kind &&
        error.operationID === preview.operation_id &&
        !error.message.includes('synthetic private text')
    )
  }
  for (const status of [400, 401, 403, 404, 409, 410, 429, 503]) {
    for (const data of [
      {},
      '<html>synthetic proxy error</html>',
      { error: { code: 'ADMIN_ABUSE_CONFLICT', details: 'extra' } },
      { error: { code: 'ADMIN_ABUSE_CONFLICT' }, success: false },
    ]) {
      api.defaults.adapter = async (config) => {
        writes++
        throw new AxiosError(
          'synthetic response',
          'ERR_BAD_RESPONSE',
          config,
          undefined,
          { ...response(config, data), status }
        )
      }
      await assert.rejects(
        commitHoneypotUsers(preview, scope),
        (error) =>
          error instanceof HoneypotFailure &&
          error.kind === 'unknown' &&
          error.operationID === preview.operation_id
      )
    }
  }
  assert.equal(writes, cases.length + 8 * 4)
})

test('original-operation query failures cannot prove rollback or switch routes', async () => {
  installRoot()
  const cases = [
    [401, { error: { code: 'ADMIN_ABUSE_UNAUTHORIZED' } }, 'forbidden'],
    [403, { error: { code: 'ADMIN_ABUSE_FORBIDDEN' } }, 'forbidden'],
    [404, { error: { code: 'ADMIN_ABUSE_OPERATION_NOT_FOUND' } }, 'not_found'],
    [404, { error: { code: 'ADMIN_ABUSE_NOT_FOUND' } }, 'unknown'],
    [403, {}, 'unknown'],
    [404, {}, 'unknown'],
    [503, { error: { code: 'ADMIN_ABUSE_UNAVAILABLE' } }, 'unknown'],
  ] as const
  const calls: InternalAxiosRequestConfig[] = []
  for (const read of [getHoneypotUserOperation, getHoneypotOperation]) {
    for (const [status, data, kind] of cases) {
      api.defaults.adapter = async (config) => {
        calls.push(config)
        throw new AxiosError(
          'synthetic lookup',
          'ERR_BAD_RESPONSE',
          config,
          undefined,
          { ...response(config, data), status }
        )
      }
      await assert.rejects(
        read(preview.operation_id, scope),
        (error) =>
          error instanceof HoneypotFailure &&
          error.kind === kind &&
          error.operationID === preview.operation_id
      )
    }
  }
  assert.equal(calls.length, cases.length * 2)
  assert.ok(calls.every((call) => call.method === 'get'))
  assert.ok(
    calls
      .slice(0, cases.length)
      .every(
        (call) =>
          call.url === `/core/v2/admin/abuse/operations/${preview.operation_id}`
      )
  )
  assert.ok(
    calls
      .slice(cases.length)
      .every(
        (call) =>
          call.url === `/core/v1/admin/abuse/operations/${preview.operation_id}`
      )
  )
})
