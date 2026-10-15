/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
// @ts-expect-error Bun's test module is only available in the test runtime.
import { afterEach, expect, test } from 'bun:test'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import {
  isCurrentNativeUsersScope,
  nativeUsersAvailable,
  readNativeUsersAvailability,
  readNativeUsersScope,
} from './access'

const originalAdapter = api.defaults.adapter
const previousAuth = useAuthStore.getState().auth
const scope = {
  userId: 901,
  sessionId: 'synthetic-native-users-session',
  role: 100 as const,
}
const available = {
  success: true,
  data: {
    admin_users_protocol: 'core.admin-users.v1',
    capabilities: { admin_users_read: true },
  },
}

function login(role = 100, id = 901, sid = scope.sessionId) {
  useAuthStore.getState().auth.setBundle({
    access_token: 'synthetic-native-users-credential',
    token_type: 'Bearer',
    access_expires_at: 2000000000,
    user: { id, role, username: 'synthetic-admin' },
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

test('only an exact successful live native tuple enables user reads', () => {
  expect(nativeUsersAvailable(available)).toBe(true)
  for (const payload of [
    null,
    {},
    { data: available.data },
    { ...available, success: false },
    {
      ...available,
      data: { ...available.data, admin_users_protocol: 'unknown' },
    },
    { ...available, data: { capabilities: available.data.capabilities } },
    {
      ...available,
      data: { ...available.data, capabilities: { admin_users_read: 'true' } },
    },
    {
      ...available,
      data: { ...available.data, capabilities: { admin_users_read: false } },
    },
    { ...available, data: { ...available.data, capabilities: null } },
  ]) {
    expect(nativeUsersAvailable(payload)).toBe(false)
  }
})

test('actual admin roles and a current safe actor/session are required', () => {
  for (const role of [10, 100]) {
    login(role)
    expect(readNativeUsersScope()?.role).toBe(role)
  }
  for (const role of [0, 1, 11, 99, 101]) {
    login(role)
    expect(readNativeUsersScope()).toBeUndefined()
  }
  login(100, Number.MAX_SAFE_INTEGER + 1)
  expect(readNativeUsersScope()).toBeUndefined()
  login(100, 901, '')
  expect(readNativeUsersScope()).toBeUndefined()
})

test('scope owns user, SID and current role including same-SID demotion', () => {
  login()
  expect(isCurrentNativeUsersScope(scope)).toBe(true)
  login(10)
  expect(isCurrentNativeUsersScope(scope)).toBe(false)
  login(100, 901, 'synthetic-other-session')
  expect(isCurrentNativeUsersScope(scope)).toBe(false)
  login(100, 902)
  expect(isCurrentNativeUsersScope(scope)).toBe(false)
})

test('capability reads use only scoped public status without auth refresh', async () => {
  login()
  const seen: string[] = []
  api.defaults.adapter = async (config) => {
    seen.push(config.url ?? '')
    expect(config.authScope).toEqual({
      userId: 901,
      sessionId: scope.sessionId,
    })
    expect(config.skipAuthRefresh).toBe(true)
    expect(config.timeout).toBe(10000)
    return {
      config,
      data: available,
      headers: {},
      status: 200,
      statusText: 'OK',
    }
  }
  expect(await readNativeUsersAvailability(scope)).toBe(true)
  expect(seen).toEqual(['/api/status'])
})

test('a late status response after demotion is discarded', async () => {
  login()
  api.defaults.adapter = async (config) => {
    login(1)
    return {
      config,
      data: available,
      headers: {},
      status: 200,
      statusText: 'OK',
    }
  }
  await expect(readNativeUsersAvailability(scope)).rejects.toMatchObject({
    kind: 'session_changed',
  })
})

test('unavailable status is a failure and cannot trigger another endpoint', async () => {
  login()
  const seen: string[] = []
  api.defaults.adapter = async (config) => {
    seen.push(config.url ?? '')
    throw Object.assign(new Error('synthetic private backend diagnostic'), {
      response: { status: 503 },
    })
  }
  await expect(readNativeUsersAvailability(scope)).rejects.toMatchObject({
    kind: 'unavailable',
  })
  expect(seen).toEqual(['/api/status'])
})
