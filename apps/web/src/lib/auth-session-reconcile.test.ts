/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { QueryClient } from '@tanstack/react-query'
import type { AxiosAdapter } from 'axios'

import { login } from '@/features/auth/api'
import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

import { api, enable2FA } from './api'
import {
  isAuthCookieIdle,
  withAuthCookieActivity,
  withAuthCookieLock,
} from './auth-cookie'
import {
  applyAuthBundle,
  bindAuthCache,
  clearAuthentication,
  setDevelopmentAuthRefreshAdapter,
} from './auth-session'
import { createAuthSessionReconciler } from './auth-session-reconcile'
import {
  subscribeAuthSessionEvents,
  type AuthSessionSyncEvent,
} from './auth-session-sync'

const descriptors = new Map(
  ['window', 'navigator', 'isSecureContext'].map((key) => [
    key,
    Object.getOwnPropertyDescriptor(globalThis, key),
  ])
)
const storage = new Map<string, string>()
let tail: Promise<void> = Promise.resolve()
const locks = {
  async request<T>(
    _name: string,
    _options: unknown,
    callback: () => Promise<T>
  ): Promise<T> {
    const previous = tail
    let release!: () => void
    tail = new Promise<void>((resolve) => {
      release = resolve
    })
    await previous
    try {
      return await callback()
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
const original = api.defaults.adapter
const bundle = (token = 'local-B', sid = 'session-B'): AuthBundle => ({
  access_token: token,
  token_type: 'Bearer',
  access_expires_at: Math.floor(Date.now() / 1000) + 600,
  user: { id: 42, username: 'synthetic', role: 1 },
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
const event = (
  kind: AuthSessionSyncEvent['kind'] = 'authenticated',
  sid = 'foreign-A'
): AuthSessionSyncEvent => ({
  kind,
  sid,
  source: 'foreign-fixture',
  nonce: crypto.randomUUID(),
  timestamp: Date.now(),
})
function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => {
    resolve = done
  })
  return { promise, resolve }
}
afterEach(async () => {
  await tail
  assert.equal(isAuthCookieIdle(), true)
  api.defaults.adapter = original
  setDevelopmentAuthRefreshAdapter(original as AxiosAdapter)
  clearAuthentication(false, 'idle')
  assert.equal(storage.size, 0)
})
after(() => {
  for (const [key, descriptor] of descriptors) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor)
    else Reflect.deleteProperty(globalThis, key)
  }
})

test('real BroadcastChannel foreign login cannot reload a queued local login and its completed family supersedes the event', async () => {
  const foreignGate = deferred(),
    foreignEntered = deferred(),
    delivered = deferred()
  const foreignWriter = locks.request('new-api:auth-refresh', {}, async () => {
    foreignEntered.resolve()
    await foreignGate.promise
  })
  await foreignEntered.promise
  let reloads = 0,
    requests = 0,
    handled: Promise<void> | undefined
  const reconciler = createAuthSessionReconciler({
    reload: () => {
      reloads++
      assert.equal(isAuthCookieIdle(), true)
    },
    signedOut: () => {},
  })
  const stop = subscribeAuthSessionEvents((value) => {
    handled = reconciler.handle(value)
    delivered.resolve()
  })
  const channel = new BroadcastChannel('new-api:auth-session')
  api.defaults.adapter = async (config) => {
    requests++
    return {
      config,
      status: 200,
      statusText: 'fixture',
      headers: {},
      data: { success: true, data: bundle() },
    }
  }
  const local = login({ username: 'synthetic', password: 'synthetic' })
  try {
    channel.postMessage(event())
    await delivered.promise
    assert.equal(reloads, 0)
    foreignGate.resolve()
    await foreignWriter
    await local
    await handled
    assert.equal(requests, 1)
    assert.equal(reloads, 0)
    assert.equal(useAuthStore.getState().auth.session?.sid, 'session-B')
    assert.equal(storage.size, 0)
  } finally {
    foreignGate.resolve()
    await foreignWriter
    stop()
    reconciler.dispose()
    channel.close()
  }
})

test('a new writer enqueued in the idle-promise continuation gap is protected from reload', async () => {
  const gate = deferred(),
    entered = deferred(),
    responseGate = deferred()
  const active = withAuthCookieActivity(() => gate.promise)
  let reloads = 0,
    next!: Promise<unknown>
  const reconciler = createAuthSessionReconciler({
    reload: () => {
      reloads++
    },
    signedOut: () => {},
  })
  api.defaults.adapter = async (config) => {
    entered.resolve()
    await responseGate.promise
    return {
      config,
      status: 200,
      statusText: 'fixture',
      headers: {},
      data: { success: true, data: bundle() },
    }
  }
  const handled = reconciler.handle(event())
  gate.resolve()
  queueMicrotask(() =>
    queueMicrotask(() => {
      next = login({ username: 'synthetic', password: 'synthetic' })
    })
  )
  await entered.promise
  assert.equal(reloads, 0)
  responseGate.resolve()
  await Promise.all([active, next, handled])
  assert.equal(reloads, 0)
  reconciler.dispose()
})

test('management activity covers refresh preflight through rotation commit', async () => {
  const expired = bundle('expired')
  expired.access_expires_at = 1
  applyAuthBundle(expired, false)
  const refreshGate = deferred(),
    entered = deferred()
  setDevelopmentAuthRefreshAdapter(async (config) => {
    entered.resolve()
    await refreshGate.promise
    return {
      config,
      status: 200,
      statusText: 'fixture',
      headers: {},
      data: { success: true, data: bundle('prepared') },
    }
  })
  api.defaults.adapter = async (config) => ({
    config,
    status: 200,
    statusText: 'fixture',
    headers: {},
    data: { success: true, data: bundle('rotated') },
  })
  const mutation = enable2FA('123456')
  await entered.promise
  let reloads = 0
  const reconciler = createAuthSessionReconciler({
    reload: () => {
      reloads++
    },
    signedOut: () => {},
  })
  const handled = reconciler.handle(event())
  refreshGate.resolve()
  await Promise.all([mutation, handled])
  assert.equal(reloads, 0)
  assert.equal(useAuthStore.getState().auth.accessToken, 'rotated')
})

test('a delayed foreign event cannot destroy an already-committed in-memory MFA challenge', async () => {
  useAuthStore
    .getState()
    .auth.setPending2FAFlowToken(
      'local-challenge',
      Math.floor(Date.now() / 1000) + 300
    )
  let reloads = 0
  const reconciler = createAuthSessionReconciler({
    reload: () => {
      reloads++
    },
    signedOut: () => {},
  })
  await reconciler.handle(event())
  assert.equal(reloads, 0)
  assert.equal(
    useAuthStore.getState().auth.pending2FAFlowToken,
    'local-challenge'
  )
})

test('sign-out waits for local rotation, then invalidates only the matching SID', async () => {
  applyAuthBundle(bundle(), false)
  const gate = deferred()
  const active = withAuthCookieLock(async () => {
    await gate.promise
    applyAuthBundle(bundle('rotated'), false)
  })
  let signedOut = 0
  const reconciler = createAuthSessionReconciler({
    reload: () => {},
    signedOut: () => {
      signedOut++
    },
  })
  const handled = reconciler.handle(event('signed_out', 'session-B'))
  assert.equal(signedOut, 0)
  gate.resolve()
  await Promise.all([active, handled])
  assert.equal(signedOut, 1)
  assert.equal(useAuthStore.getState().auth.user, null)
})

test('real password login B retains its pending MFA flow when BroadcastChannel signs out the older A session', async () => {
  const oldAccount = bundle('old-A', 'session-A')
  oldAccount.user = { ...oldAccount.user, id: 41 }
  applyAuthBundle(oldAccount, false)
  const expiresAt = Math.floor(Date.now() / 1000) + 300
  api.defaults.adapter = async (config) => ({
    config,
    status: 200,
    statusText: 'fixture',
    headers: {},
    data: {
      success: true,
      data: {
        require_2fa: true,
        flow_token: 'pending-B',
        expires_at: expiresAt,
      },
    },
  })
  await login({ username: 'account-B', password: 'synthetic' })
  assert.equal(useAuthStore.getState().auth.session?.sid, 'session-A')
  const cache = new QueryClient()
  cache.setQueryData(['old-A-private'], 'old-account-data')
  const unbind = bindAuthCache(cache)
  const transitions: Array<string | null> = []
  const stopStore = useAuthStore.subscribe((state) => {
    transitions.push(state.auth.pending2FAFlowToken)
  })
  let signedOut = 0,
    handled: Promise<void> | undefined
  const delivered = deferred()
  const reconciler = createAuthSessionReconciler({
    reload: () => assert.fail('a pending local challenge must not reload'),
    signedOut: () => {
      signedOut++
    },
  })
  const stop = subscribeAuthSessionEvents((value) => {
    handled = reconciler.handle(value)
    delivered.resolve()
  })
  const channel = new BroadcastChannel('new-api:auth-session')
  try {
    channel.postMessage(event('signed_out', 'session-A'))
    await delivered.promise
    await handled
    const auth = useAuthStore.getState().auth
    assert.equal(auth.user, null)
    assert.equal(auth.session, null)
    assert.equal(auth.accessToken, null)
    assert.equal(auth.accessExpiresAt, null)
    assert.equal(cache.getQueryData(['old-A-private']), undefined)
    assert.equal(auth.pending2FAFlowToken, 'pending-B')
    assert.equal(auth.pending2FAFlowExpiresAt, expiresAt)
    assert.equal(signedOut, 0)
    assert.deepEqual(transitions, ['pending-B'])
    assert.equal(storage.size, 0)
  } finally {
    stop()
    stopStore()
    unbind()
    reconciler.dispose()
    channel.close()
    cache.clear()
  }
})

test('sign-out discards an expired pending flow and retains the sign-in navigation', async () => {
  applyAuthBundle(bundle(), false)
  useAuthStore.getState().auth.setPending2FAFlowToken('expired-flow', 1)
  let signedOut = 0
  const reconciler = createAuthSessionReconciler({
    reload: () => {},
    signedOut: () => {
      signedOut++
    },
  })
  await reconciler.handle(event('signed_out', 'session-B'))
  assert.equal(useAuthStore.getState().auth.pending2FAFlowToken, null)
  assert.equal(signedOut, 1)
})

test('an idle tab with expired flow retains foreign-family reload behavior and same-family events are ignored', async () => {
  applyAuthBundle(bundle(), false)
  let reloads = 0
  const reconciler = createAuthSessionReconciler({
    reload: () => {
      reloads++
      assert.equal(storage.size, 0)
    },
    signedOut: () => {},
  })
  await reconciler.handle(event('authenticated', 'session-B'))
  assert.equal(reloads, 0)
  useAuthStore.getState().auth.setPending2FAFlowToken('expired-flow', 1)
  await reconciler.handle(event())
  assert.equal(reloads, 1)
  assert.equal(useAuthStore.getState().auth.user, null)
  assert.equal(useAuthStore.getState().auth.pending2FAFlowToken, null)
  let dispatched = false
  await assert.rejects(
    withAuthCookieLock(async () => {
      dispatched = true
    })
  )
  assert.equal(dispatched, false)
  assert.equal(storage.size, 0)
})
