/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'

import { AxiosError, type InternalAxiosRequestConfig } from 'axios'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import {
  commitHoneypotUsers,
  getHoneypotOperation,
  getHoneypotUser,
  getHoneypotUserOperation,
  getHoneypotUsers,
  HoneypotFailure,
  previewHoneypotUsers,
  userResultMatchesPreview,
} from './api'
import {
  honeypotUserIDSchema,
  previewMatchesRequest,
  previewSchema,
  resultSchema,
  userDetailSchema,
  userListSchema,
  type HoneypotPreview,
  type HoneypotPreviewRequest,
  type HoneypotResult,
} from './contract'

const first = '9007199254740992'
const adjacent = '9007199254740993'
const maximum = '9223372036854775807'
const scope = { userId: 901, sessionId: 'synthetic-wire-v2-session' }
const originalAdapter = api.defaults.adapter
const previousAuth = useAuthStore.getState().auth
const user = (user_id: string) => ({
  user_id,
  username: 'synthetic-user',
  email: 'synthetic@example.invalid',
  active: true,
  platform_role: 'user' as const,
  revision: 0,
  marked: false,
  policy_id: null,
})
const preview: HoneypotPreview = {
  version: 2,
  operation_id: 'synthetic-wire-v2-operation',
  preview_id: 'opaque-preview',
  preview_hash: 'a'.repeat(64),
  expires_at: 2000000000,
  action: 'mark',
  policy_id: 'fixed',
  policy_revision: 4,
  targets: [first, adjacent].map((user_id) => ({
    user_id,
    username: 'synthetic-user',
    active: true,
    expected_revision: 0,
    before: { marked: false, policy_id: null },
    after: { marked: true, policy_id: 'fixed', active: true },
  })),
}
const receipt: HoneypotResult = {
  version: 2,
  status: 'applied',
  kind: 'users',
  operation_id: preview.operation_id,
  policy_revision: 4,
  users: [first, adjacent].map((id) => ({
    ...user(id),
    revision: 1,
    marked: true,
    policy_id: 'fixed',
  })),
}
function login() {
  useAuthStore.getState().auth.setBundle({
    access_token: 'private-synthetic-unit-token',
    token_type: 'Bearer',
    access_expires_at: 2000000000,
    user: { id: scope.userId, username: 'synthetic-root', role: 100 },
    session: {
      sid: scope.sessionId,
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'unit-only',
      created_at: 1,
      last_active_at: 1,
      expires_at: 2000000000,
    },
  })
}
const response = (config: InternalAxiosRequestConfig, data: unknown) => ({
  status: 200,
  statusText: 'OK',
  headers: {},
  config,
  data,
})
afterEach(() => {
  api.defaults.adapter = originalAdapter
  useAuthStore.setState({ auth: previousAuth })
})

test('v2 IDs preserve adjacent large integers and max i64, refusing all numeric coercions', () => {
  for (const id of ['7', first, adjacent, maximum]) {
    assert.equal(honeypotUserIDSchema.parse(id), id)
  }
  for (const id of [
    7,
    Number(adjacent),
    0,
    null,
    '',
    '0',
    '07',
    '+7',
    '-7',
    ' 7',
    '7 ',
    '7e0',
    '7.0',
    '７',
    '9223372036854775808',
  ]) {
    assert.equal(honeypotUserIDSchema.safeParse(id).success, false, String(id))
    assert.equal(
      userListSchema.safeParse({
        version: 2,
        users: [{ ...user('7'), user_id: id }],
        next_after: null,
      }).success,
      false
    )
  }
  assert.equal(
    userListSchema.safeParse({
      version: 1,
      users: [{ ...user('7'), user_id: Number(adjacent) }],
      next_after: null,
    }).success,
    false
  )
})

test('v2 list ordering is numeric and every continuation cursor binds the final exact ID', () => {
  const list = {
    version: 2,
    users: ['7', '10', first, adjacent, maximum].map(user),
    next_after: maximum,
  }
  assert.deepEqual(userListSchema.parse(list), list)
  for (const bad of [
    { ...list, users: [user('10'), user('7')] },
    { ...list, users: [user(first), user(first)], next_after: first },
    { ...list, next_after: adjacent },
    { ...list, users: [], next_after: maximum },
    { ...list, next_after: 7 },
    { ...list, users: [{ ...user(maximum), version: 2 }] },
  ]) {
    assert.equal(userListSchema.safeParse(bad).success, false)
  }
  assert.equal(userDetailSchema.safeParse(user(adjacent)).success, false)
  assert.equal(
    userDetailSchema.parse({ ...user(adjacent), version: 2 }).user_id,
    adjacent
  )
})

test('first page omits after on the actual Axios wire and later cursors stay exact', async () => {
  login()
  const calls: InternalAxiosRequestConfig[] = []
  api.defaults.adapter = async (config) => {
    calls.push(config)
    return response(config, {
      version: 2,
      users: [user(adjacent)],
      next_after: null,
    })
  }
  await getHoneypotUsers(scope, {
    after: null,
    limit: 50,
    query: '',
    state: 'all',
  })
  await getHoneypotUsers(scope, {
    after: first,
    limit: 50,
    query: '',
    state: 'all',
  })
  assert.equal(calls[0].url, '/core/v2/admin/abuse/users')
  assert.equal(
    new URL(api.getUri(calls[0]), 'https://example.invalid').searchParams.has(
      'after'
    ),
    false
  )
  assert.equal(
    new URL(api.getUri(calls[1]), 'https://example.invalid').searchParams.get(
      'after'
    ),
    first
  )
  for (const after of ['0', '07', `${maximum}0`]) {
    await assert.rejects(
      getHoneypotUsers(scope, { after, limit: 50, query: '', state: 'all' }),
      (error) => error instanceof HoneypotFailure && error.kind === 'invalid'
    )
  }
  assert.equal(calls.length, 2)
  await assert.rejects(
    getHoneypotUsers(scope, {
      after: adjacent,
      limit: 50,
      query: '',
      state: 'all',
    }),
    HoneypotFailure
  )
  assert.equal(calls.length, 3)
})

test('detail marker and exact target identity are required without fallback', async () => {
  login()
  const calls: string[] = []
  let data: unknown = { ...user(adjacent), version: 2 }
  api.defaults.adapter = async (config) => {
    calls.push(config.url ?? '')
    return response(config, data)
  }
  assert.equal((await getHoneypotUser(adjacent, scope)).user_id, adjacent)
  data = { ...user(first), version: 2 }
  await assert.rejects(getHoneypotUser(adjacent, scope), HoneypotFailure)
  data = user(adjacent)
  await assert.rejects(getHoneypotUser(adjacent, scope), HoneypotFailure)
  await assert.rejects(getHoneypotUser('0', scope), HoneypotFailure)
  assert.deepEqual(
    calls,
    Array(3).fill(`/core/v2/admin/abuse/users/${adjacent}`)
  )
})

test('preview keeps original target order and commit transmits only the opaque server binding', async () => {
  login()
  const request: HoneypotPreviewRequest = {
    operation_id: preview.operation_id,
    reason: 'Synthetic reviewed case',
    action: 'mark',
    policy_id: 'fixed',
    expected_policy_revision: 4,
    targets: [adjacent, first].map((user_id) => ({
      user_id,
      expected_revision: 0,
    })),
  }
  const calls: InternalAxiosRequestConfig[] = []
  api.defaults.adapter = async (config) => {
    calls.push(config)
    return response(
      config,
      config.url?.endsWith('/preview') ? preview : receipt
    )
  }
  const reviewed = await previewHoneypotUsers(request, scope)
  assert.deepEqual(JSON.parse(calls[0].data), request)
  assert.equal(calls[0].url, '/core/v2/admin/abuse/users/preview')
  assert.equal(previewMatchesRequest(reviewed, request), true)
  await commitHoneypotUsers(reviewed, scope)
  assert.deepEqual(JSON.parse(calls[1].data), {
    operation_id: preview.operation_id,
    preview_id: preview.preview_id,
    preview_hash: preview.preview_hash,
  })
  assert.equal(calls[1].url, '/core/v2/admin/abuse/users/commit')
  const forged = {
    ...request,
    targets: [{ user_id: Number(adjacent), expected_revision: 0 }],
  } as unknown as HoneypotPreviewRequest
  assert.equal(previewMatchesRequest(preview, forged), false)
  await assert.rejects(previewHoneypotUsers(forged, scope), HoneypotFailure)
  assert.equal(calls.length, 2)
})

test('reconciliation distinguishes adjacent IDs and refuses forged numeric or v1 users receipts', () => {
  assert.equal(userResultMatchesPreview(receipt, preview), true)
  assert.equal(
    userResultMatchesPreview(
      {
        ...receipt,
        users: receipt.users.map((row) => ({ ...row, user_id: first })),
      },
      preview
    ),
    false
  )
  for (const wrong of [
    { ...receipt, version: 1 },
    {
      ...receipt,
      users: receipt.users.map((row) => ({
        ...row,
        user_id: Number(row.user_id),
      })),
    },
    { ...receipt, extra: true },
  ]) {
    assert.equal(resultSchema.safeParse(wrong).success, false)
    assert.equal(
      userResultMatchesPreview(wrong as unknown as HoneypotResult, preview),
      false
    )
  }
  assert.equal(
    previewSchema.safeParse({ ...preview, version: 1 }).success,
    false
  )
})

test('known user and policy operations select their exact routes before validating kind/version', async () => {
  login()
  const policy = {
    version: 1,
    status: 'applied',
    operation_id: preview.operation_id,
    kind: 'policies',
    policy_revision: 4,
    users: [],
    applied_config_hash: 'b'.repeat(64),
  }
  const calls: string[] = []
  let data: unknown = receipt
  api.defaults.adapter = async (config) => {
    calls.push(config.url ?? '')
    return response(config, data)
  }
  await getHoneypotUserOperation(preview.operation_id, scope)
  data = policy
  await getHoneypotOperation(preview.operation_id, scope)
  await assert.rejects(
    getHoneypotUserOperation(preview.operation_id, scope),
    (error) =>
      error instanceof HoneypotFailure &&
      error.kind === 'unknown' &&
      error.operationID === preview.operation_id
  )
  data = { ...receipt, version: 1 }
  await assert.rejects(
    getHoneypotUserOperation(preview.operation_id, scope),
    HoneypotFailure
  )
  assert.deepEqual(
    calls,
    ['v2', 'v1', 'v2', 'v2'].map(
      (version) =>
        `/core/${version}/admin/abuse/operations/${preview.operation_id}`
    )
  )
})

test('an unknown commit and unavailable original operation never dispatch a second write', async () => {
  login()
  const calls: InternalAxiosRequestConfig[] = []
  api.defaults.adapter = async (config) => {
    calls.push(config)
    throw new AxiosError('synthetic unavailable', 'ETIMEDOUT', config)
  }
  await assert.rejects(
    commitHoneypotUsers(preview, scope),
    (error) =>
      error instanceof HoneypotFailure &&
      error.kind === 'unknown' &&
      error.operationID === preview.operation_id
  )
  await assert.rejects(
    getHoneypotUserOperation(preview.operation_id, scope),
    HoneypotFailure
  )
  assert.deepEqual(
    calls.map((call) => call.method),
    ['post', 'get']
  )
})
