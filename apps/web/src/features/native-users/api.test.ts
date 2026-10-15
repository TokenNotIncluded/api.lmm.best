/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
// @ts-expect-error Bun's test module is only available in the test runtime.
import { afterEach, expect, test } from 'bun:test'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { readNativeUsersAvailability } from './access'
import { readNativeUserGroups, readNativeUsers } from './api'
import { DEFAULT_NATIVE_USERS_QUERY } from './contract'
import { nativeListFixture } from './test-fixtures'

const originalAdapter = api.defaults.adapter
const previousAuth = useAuthStore.getState().auth
const scope = {
  userId: 901,
  sessionId: 'synthetic-native-api-session',
  role: 10 as const,
}
function login(role = 10, sid = scope.sessionId) {
  useAuthStore.getState().auth.setBundle({
    access_token: 'synthetic-native-api-credential',
    token_type: 'Bearer',
    access_expires_at: 2000000000,
    user: { id: 901, role, username: 'synthetic-admin' },
    session: {
      sid,
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'synthetic',
      created_at: 1,
      last_active_at: 1,
      expires_at: 2000000000,
    },
  })
}
afterEach(() => {
  api.defaults.adapter = originalAdapter
  useAuthStore.setState({ auth: previousAuth })
})

test('native reads bind only supported literal query values to the current Session scope', async () => {
  login()
  const seen: string[] = []
  api.defaults.adapter = async (config) => {
    seen.push(`${config.method} ${config.url}`)
    expect(config.params).toEqual({
      ...DEFAULT_NATIVE_USERS_QUERY,
      keyword: '%_',
      group: ' exact ',
      service_level: '0',
    })
    expect(config.authScope).toEqual({
      userId: 901,
      sessionId: scope.sessionId,
    })
    expect(config.skipAuthRefresh).toBe(true)
    expect(config.disableDuplicate).toBe(true)
    expect(config.timeout).toBe(10000)
    return {
      config,
      data: nativeListFixture(),
      headers: {},
      status: 200,
      statusText: 'OK',
    }
  }
  const result = await readNativeUsers(scope, {
    ...DEFAULT_NATIVE_USERS_QUERY,
    keyword: ' %_ ',
    group: ' exact ',
    service_level: '0',
  })
  expect(result.items[0].id).toBe('9007199254740993')
  expect(seen).toEqual(['get /core/v1/admin/users'])
})

test('legacy filters and unsafe queries fail before any network operation', async () => {
  login()
  let calls = 0
  api.defaults.adapter = async () => {
    calls++
    throw new Error('not permitted')
  }
  await expect(
    readNativeUsers(scope, {
      ...DEFAULT_NATIVE_USERS_QUERY,
      risk: 'high',
    } as typeof DEFAULT_NATIVE_USERS_QUERY)
  ).rejects.toMatchObject({ kind: 'invalid_query' })
  await expect(readNativeUserGroups(scope, 0)).rejects.toMatchObject({
    kind: 'invalid_query',
  })
  expect(calls).toBe(0)
})

test('group choices have independent exact paging rather than a synthetic full list', async () => {
  login()
  api.defaults.adapter = async (config) => {
    expect(config.url).toBe('/core/v1/admin/users/groups')
    expect(config.params).toEqual({ page: 2, page_size: 20 })
    return {
      config,
      data: {
        version: 1,
        page: 2,
        page_size: 20,
        total: 21,
        items: ['last-group'],
        source: 'enabled_explicit_user_routing_grants',
      },
      headers: {},
      status: 200,
      statusText: 'OK',
    }
  }
  expect((await readNativeUserGroups(scope, 2)).items).toEqual(['last-group'])
})

test('incompatible envelopes and mismatched requested pages cannot become valid or empty results', async () => {
  login()
  for (const payload of [
    { success: true, data: nativeListFixture() },
    { ...nativeListFixture(), page: 2, total: 21 },
    { ...nativeListFixture(), capabilities: null },
  ]) {
    api.defaults.adapter = async (config) => ({
      config,
      data: payload,
      headers: {},
      status: 200,
      statusText: 'OK',
    })
    await expect(
      readNativeUsers(scope, DEFAULT_NATIVE_USERS_QUERY)
    ).rejects.toMatchObject({ kind: 'invalid_response' })
  }
})

test('real denial and unavailable statuses never try legacy endpoints or replay writes', async () => {
  login()
  for (const [status, kind] of [
    [401, 'forbidden'],
    [403, 'forbidden'],
    [404, 'unavailable'],
    [422, 'unavailable'],
    [429, 'limited'],
    [503, 'unavailable'],
  ] as const) {
    const seen: string[] = []
    api.defaults.adapter = async (config) => {
      seen.push(`${config.method} ${config.url}`)
      throw Object.assign(
        new Error('synthetic private query or internal diagnostic'),
        { response: { status } }
      )
    }
    await expect(
      readNativeUsers(scope, DEFAULT_NATIVE_USERS_QUERY)
    ).rejects.toMatchObject({ kind })
    expect(seen).toEqual(['get /core/v1/admin/users'])
  }
})

test('late native list and group responses lose ownership after SID or role changes', async () => {
  for (const changed of ['sid', 'role']) {
    login()
    api.defaults.adapter = async (config) => {
      if (changed === 'sid') login(10, 'synthetic-replacement-session')
      else login(1)
      return {
        config,
        data: config.url?.endsWith('/groups')
          ? {
              version: 1,
              page: 1,
              page_size: 20,
              total: 0,
              items: [],
              source: 'enabled_explicit_user_routing_grants',
            }
          : nativeListFixture(),
        headers: {},
        status: 200,
        statusText: 'OK',
      }
    }
    await expect(
      readNativeUsers(scope, DEFAULT_NATIVE_USERS_QUERY)
    ).rejects.toMatchObject({ kind: 'session_changed' })
    login()
    await expect(readNativeUserGroups(scope, 1)).rejects.toMatchObject({
      kind: 'session_changed',
    })
  }
})

test('aborted reads and status responses stay scoped without a retry or a misleading denial', async () => {
  login()
  let calls = 0
  for (const read of [
    (signal: AbortSignal) =>
      readNativeUsers(scope, DEFAULT_NATIVE_USERS_QUERY, signal),
    (signal: AbortSignal) => readNativeUserGroups(scope, 1, signal),
    (signal: AbortSignal) => readNativeUsersAvailability(scope, signal),
  ]) {
    const controller = new AbortController()
    api.defaults.adapter = async () => {
      calls++
      controller.abort()
      throw Object.assign(
        new Error('synthetic private denial after cancellation'),
        { response: { status: 403 } }
      )
    }
    await expect(read(controller.signal)).rejects.toMatchObject({
      kind: 'session_changed',
    })
  }
  expect(calls).toBe(3)
})

test('an unset service level is absent from the actual URI, while native levels zero and four remain explicit', async () => {
  login()
  const uris: string[] = []
  api.defaults.adapter = async (config) => {
    uris.push(api.getUri(config))
    return {
      config,
      data: nativeListFixture(),
      headers: {},
      status: 200,
      statusText: 'OK',
    }
  }
  for (const level of ['', '0', '4'] as const) {
    await readNativeUsers(scope, {
      ...DEFAULT_NATIVE_USERS_QUERY,
      service_level: level,
    })
  }
  const params = uris.map(
    (uri) => new URL(uri, 'https://synthetic.example.invalid').searchParams
  )
  expect(params[0].has('service_level')).toBe(false)
  expect(params[1].get('service_level')).toBe('0')
  expect(params[2].get('service_level')).toBe('4')
})
