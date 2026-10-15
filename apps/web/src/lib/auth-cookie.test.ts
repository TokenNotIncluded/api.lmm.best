/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { AxiosError, type AxiosAdapter, type AxiosResponse } from 'axios'

import { executeAssistantUserAction } from '@/features/assistant/api'
import { login, login2fa, logout } from '@/features/auth/api'
import { updateUserProfile } from '@/features/profile/api'
import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

import {
  api,
  enable2FA,
  disable2FA,
  regenerate2FABackupCodes,
  setup2FA,
} from './api'
import {
  AUTH_COOKIE_LOCK,
  AuthCookieBlockedError,
  withAuthCookieLock,
} from './auth-cookie'
import {
  applyAuthBundle,
  clearAuthentication,
  refreshAuthentication,
  setDevelopmentAuthRefreshAdapter,
} from './auth-session'

const descriptors = new Map(
  ['window', 'navigator', 'isSecureContext'].map((key) => [
    key,
    Object.getOwnPropertyDescriptor(globalThis, key),
  ])
)
const storage = new Map<string, string>()
let held = 0
const names: string[] = []
let tail: Promise<unknown> = Promise.resolve()
const locks = {
  async request<T>(
    name: string,
    options: { signal?: AbortSignal },
    callback: () => Promise<T>
  ): Promise<T> {
    names.push(name)
    const predecessor = tail
    let release!: () => void
    tail = new Promise<void>((resolve) => {
      release = resolve
    })
    await predecessor
    try {
      if (options.signal?.aborted) {
        throw new DOMException('Aborted', 'AbortError')
      }
      held++
      try {
        return await callback()
      } finally {
        held--
      }
    } finally {
      release()
    }
  },
}
Object.defineProperty(globalThis, 'isSecureContext', {
  configurable: true,
  value: true,
})
Object.defineProperty(globalThis, 'navigator', {
  configurable: true,
  value: { locks },
})
Object.defineProperty(globalThis, 'window', {
  configurable: true,
  value: {
    localStorage: {
      getItem: (key: string) => storage.get(key) ?? null,
      setItem: (key: string, value: string) => storage.set(key, value),
      removeItem: (key: string) => storage.delete(key),
    },
  },
})
const originalAdapter = api.defaults.adapter
const bundle = (token = 'initial', id = 42, sid = 'session'): AuthBundle => ({
  access_token: token,
  token_type: 'Bearer',
  access_expires_at: Math.floor(Date.now() / 1000) + 600,
  user: { id, username: 'synthetic', role: 1 },
  session: {
    sid,
    current: true,
    login_method: 'password',
    ip: '',
    user_agent: '',
    created_at: 1,
    last_active_at: 1,
    expires_at: 2_000_000_000,
  },
})
function response(
  config: Parameters<AxiosAdapter>[0],
  status: number,
  data: unknown
): AxiosResponse {
  return { config, status, statusText: 'fixture', data, headers: {} }
}
function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => {
    resolve = done
  })
  return { promise, resolve }
}
afterEach(async () => {
  await tail
  assert.equal(held, 0)
  assert.ok(names.every((name) => name === AUTH_COOKIE_LOCK))
  names.length = 0
  api.defaults.adapter = originalAdapter
  setDevelopmentAuthRefreshAdapter(originalAdapter as AxiosAdapter)
  clearAuthentication(false, 'idle')
})
after(() => {
  for (const [key, descriptor] of descriptors) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor)
    else Reflect.deleteProperty(globalThis, key)
  }
})

test('password login commits before a queued cookie writer can enter', async () => {
  const entered = deferred(),
    release = deferred()
  api.defaults.adapter = async (config) => {
    assert.equal(held, 1)
    assert.ok(
      [...storage.values()].some((value) => value.startsWith('pending:'))
    )
    entered.resolve()
    await release.promise
    return response(config, 200, { success: true, data: bundle('login') })
  }
  const first = login({
    username: 'synthetic',
    password: 'not-a-real-password',
  })
  await entered.promise
  const next = withAuthCookieLock(async () => {
    assert.equal(useAuthStore.getState().auth.accessToken, 'login')
    assert.equal(storage.size, 0)
  })
  release.resolve()
  await Promise.all([first, next])
})

test('OTP waiting cancellation sends nothing; active cancellation retains lock through commit', async () => {
  useAuthStore.getState().auth.setPending2FAFlowToken('flow')
  const blocker = deferred(),
    entered = deferred()
  const hold = withAuthCookieLock(async () => {
    entered.resolve()
    await blocker.promise
  })
  await entered.promise
  let requests = 0
  api.defaults.adapter = async (config) => {
    requests++
    return response(config, 200, { success: true, data: bundle('otp') })
  }
  const waitingController = new AbortController()
  const cancelled = assert.rejects(
    login2fa({ code: '123456', flow_token: 'flow' }, waitingController.signal)
  )
  waitingController.abort()
  blocker.resolve()
  await Promise.all([hold, cancelled])
  assert.equal(requests, 0)
  const active = new AbortController(),
    started = deferred(),
    release = deferred()
  api.defaults.adapter = async (config) => {
    requests++
    assert.equal(config.signal, undefined)
    started.resolve()
    await release.promise
    assert.equal(held, 1)
    return response(config, 200, { success: true, data: bundle('otp') })
  }
  const pending = assert.rejects(
    login2fa({ code: '123456', flow_token: 'flow' }, active.signal)
  )
  await started.promise
  active.abort()
  useAuthStore.getState().auth.setPending2FAFlowToken(null)
  let nextEntered = false
  const next = withAuthCookieLock(async () => {
    nextEntered = true
    assert.equal(useAuthStore.getState().auth.accessToken, 'otp')
  })
  await Promise.resolve()
  assert.equal(nextEntered, false)
  release.resolve()
  await Promise.all([pending, next])
  assert.equal(requests, 1)
})

test('management 401 releases its lock before refreshing and never replays input', async () => {
  applyAuthBundle(bundle(), false)
  let attempts = 0,
    refreshes = 0
  api.defaults.adapter = async (config) => {
    assert.equal(held, 1)
    assert.equal(config.skipAuthRefresh, true)
    attempts++
    throw new AxiosError(
      'rejected',
      'ERR_BAD_REQUEST',
      config,
      undefined,
      response(config, 401, {})
    )
  }
  setDevelopmentAuthRefreshAdapter(async (config) => {
    assert.equal(held, 1)
    refreshes++
    return response(config, 200, { success: true, data: bundle('refreshed') })
  })
  await assert.rejects(enable2FA('123456'))
  assert.equal(attempts, 1)
  assert.equal(refreshes, 1)
  assert.equal(useAuthStore.getState().auth.accessToken, 'refreshed')
  assert.equal(storage.size, 0)
})

test('password rotation and logout both commit before releasing the shared lock', async () => {
  applyAuthBundle(bundle(), false)
  api.defaults.adapter = async (config) => {
    assert.equal(held, 1)
    assert.equal(config.skipAuthRefresh, true)
    return response(config, 200, {
      success: true,
      data: config.url?.endsWith('/logout') ? undefined : bundle('password'),
    })
  }
  await updateUserProfile({
    password: 'synthetic-new-password',
    original_password: 'synthetic-old-password',
  })
  assert.equal(useAuthStore.getState().auth.accessToken, 'password')
  await executeAssistantUserAction(
    {
      type: 'user_password_change',
      requires_confirmation: true,
      target_user_id: 42,
      target_username: 'synthetic',
      target_display_name: 'Synthetic',
      target_role: 1,
      target_group: 'default',
      target_is_self: true,
    },
    {
      currentPassword: 'synthetic-old-password',
      newPassword: 'synthetic-new-password',
    }
  )
  assert.equal(useAuthStore.getState().auth.accessToken, 'password')
  await logout()
  assert.equal(useAuthStore.getState().auth.user, null)
  assert.equal(storage.size, 0)
})

test('a management request queued before a new account is never dispatched as that account', async () => {
  applyAuthBundle(bundle(), false)
  const entered = deferred(),
    release = deferred()
  const first = withAuthCookieLock(async () => {
    entered.resolve()
    await release.promise
    applyAuthBundle(bundle('new', 99, 'new-session'), false)
  })
  await entered.promise
  let attempts = 0
  api.defaults.adapter = async () => {
    attempts++
    throw new Error('unexpected dispatch')
  }
  const rejected = assert.rejects(disable2FA('123456'))
  release.resolve()
  await Promise.all([first, rejected])
  assert.equal(attempts, 0)
  assert.equal(useAuthStore.getState().auth.user?.id, 99)
})

test('a password challenge supersedes a refresh already queued behind login', async () => {
  const entered = deferred(),
    release = deferred()
  let refreshes = 0
  api.defaults.adapter = async (config) => {
    entered.resolve()
    await release.promise
    return response(config, 200, {
      success: true,
      data: { require_2fa: true, flow_token: 'challenge' },
    })
  }
  setDevelopmentAuthRefreshAdapter(async () => {
    refreshes++
    throw new Error('unexpected refresh')
  })
  const first = login({ username: 'synthetic', password: 'synthetic' })
  await entered.promise
  const queued = refreshAuthentication()
  release.resolve()
  await first
  assert.equal((await queued).kind, 'transient_error')
  assert.equal((await refreshAuthentication()).kind, 'transient_error')
  assert.equal(refreshes, 0)
  assert.equal(useAuthStore.getState().auth.pending2FAFlowToken, 'challenge')
})

test('an expiring authenticated writer refreshes before acquiring its mutation lock', async () => {
  const expiring = bundle()
  expiring.access_expires_at = 1
  applyAuthBundle(expiring, false)
  const order: string[] = []
  setDevelopmentAuthRefreshAdapter(async (config) => {
    order.push('refresh')
    assert.equal(held, 1)
    return response(config, 200, { success: true, data: bundle('prepared') })
  })
  api.defaults.adapter = async (config) => {
    order.push('mutation')
    assert.equal(held, 1)
    assert.equal(config.headers.Authorization, 'Bearer prepared')
    return response(config, 200, { success: true, data: bundle('rotated') })
  }
  await enable2FA('123456')
  assert.deepEqual(order, ['refresh', 'mutation'])
  assert.equal(storage.size, 0)
})

test('no-WebLocks compatibility fallback performs no locking or durable intent guarantee', async () => {
  Object.defineProperty(globalThis, 'navigator', {
    configurable: true,
    value: {},
  })
  try {
    api.defaults.adapter = async (config) => {
      assert.equal(held, 0)
      assert.equal(storage.size, 0)
      return response(config, 200, { success: true, data: bundle('unordered') })
    }
    await login({ username: 'synthetic', password: 'synthetic' })
  } finally {
    Object.defineProperty(globalThis, 'navigator', {
      configurable: true,
      value: { locks },
    })
  }
})

test('transport timeout keeps a durable marker and blocks every participating writer', async () => {
  applyAuthBundle(bundle(), false)
  let attempts = 0
  api.defaults.adapter = async (config) => {
    attempts++
    throw new AxiosError('fixture timeout', 'ECONNABORTED', config)
  }
  await assert.rejects(enable2FA('123456'), AuthCookieBlockedError)
  assert.ok(
    [...storage.values()].some((value) => value.startsWith('uncertain:'))
  )
  for (const write of [
    () => login({ username: 'synthetic', password: 'synthetic' }),
    () => login2fa({ code: '123456', flow_token: 'flow' }),
    () => setup2FA(),
    () => enable2FA('123456'),
    () => disable2FA('123456'),
    () => regenerate2FABackupCodes('123456'),
    () => updateUserProfile({ password: 'synthetic' }),
    () => logout(),
  ]) {
    await assert.rejects(write(), AuthCookieBlockedError)
  }
  const outcome = await refreshAuthentication()
  assert.equal(outcome.kind, 'transient_error')
  assert.equal(attempts, 1)
})
