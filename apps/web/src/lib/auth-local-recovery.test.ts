/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { QueryClient } from '@tanstack/react-query'
import type { AxiosAdapter } from 'axios'

import { logout } from '@/features/auth/api'
import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

import { api } from './api'
import {
  AuthCookieBlockedError,
  getAuthCookieBlockedState,
  withAuthCookieLock,
} from './auth-cookie'
import {
  applyAuthBundle,
  applyPending2FAFlow,
  bindAuthCache,
  bootstrapAuthentication,
  captureLocalAuthRecoveryScope,
  clearAuthentication,
  endLocalAuthSessionForRecovery,
  refreshAuthentication,
  setDevelopmentAuthRefreshAdapter,
} from './auth-session'
import { createAuthSessionReconciler } from './auth-session-reconcile'

const keys = [
  'window',
  'navigator',
  'isSecureContext',
  'BroadcastChannel',
] as const
const descriptors = new Map(
  keys.map((key) => [key, Object.getOwnPropertyDescriptor(globalThis, key)])
)
const storage = new Map<string, string>()
const published: unknown[] = []
const writes: string[] = []
let tail: Promise<unknown> = Promise.resolve()
const locks = {
  async request<T>(
    _name: string,
    _options: unknown,
    callback: () => Promise<T>
  ): Promise<T> {
    const predecessor = tail
    let release!: () => void
    tail = new Promise<void>((resolve) => {
      release = resolve
    })
    await predecessor
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
      setItem: (key: string, value: string) => {
        writes.push(key)
        storage.set(key, value)
      },
      removeItem: (key: string) => {
        writes.push(key)
        storage.delete(key)
      },
    },
  },
})
Object.defineProperty(globalThis, 'BroadcastChannel', {
  configurable: true,
  value: class {
    postMessage(value: unknown) {
      published.push(value)
    }
  },
})
const originalAdapter = api.defaults.adapter
let preMfaScope: ReturnType<typeof captureLocalAuthRecoveryScope>
const markerKey = 'new-api:auth-cookie:uncertain'
const bundle = (id = 42, sid = 'synthetic-session'): AuthBundle => ({
  access_token: 'synthetic-local-only',
  token_type: 'Bearer',
  access_expires_at: 2_000_000_000,
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
function deferred() {
  let resolve!: () => void
  const promise = new Promise<void>((done) => {
    resolve = done
  })
  return { resolve, promise }
}
after(() => {
  api.defaults.adapter = originalAdapter
  setDevelopmentAuthRefreshAdapter(originalAdapter as AxiosAdapter)
  clearAuthentication(false)
  for (const [key, descriptor] of descriptors) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor)
    else Reflect.deleteProperty(globalThis, key)
  }
})

test('healthy local recovery refuses to bypass normal server logout', () => {
  applyAuthBundle(bundle(), false)
  const result = endLocalAuthSessionForRecovery(captureLocalAuthRecoveryScope())
  assert.deepEqual(result, {
    kind: 'not_blocked',
    serverLogoutConfirmed: false,
  })
  assert.equal(useAuthStore.getState().auth.user?.id, 42)
  preMfaScope = captureLocalAuthRecoveryScope()
  applyPending2FAFlow('synthetic-mfa')
  assert.equal(
    captureLocalAuthRecoveryScope().accountWriteEpoch,
    preMfaScope.accountWriteEpoch
  )
})

test('blocked logout dispatches zero requests; explicit local recovery clears state, MFA and private cache without broadcasts or Cookie changes', async () => {
  const cache = new QueryClient()
  cache.setQueryData(['private'], 'synthetic-private')
  const unbind = bindAuthCache(cache)
  let dispatches = 0
  api.defaults.adapter = async () => {
    dispatches++
    throw new Error('must not dispatch')
  }
  storage.set(markerKey, 'pending:synthetic-crashed-writer')
  await assert.rejects(logout(), AuthCookieBlockedError)
  assert.equal(dispatches, 0)
  assert.equal(endLocalAuthSessionForRecovery(preMfaScope).kind, 'superseded')
  assert.equal(
    useAuthStore.getState().auth.pending2FAFlowToken,
    'synthetic-mfa'
  )
  const beforeWrites = writes.length
  try {
    assert.deepEqual(
      endLocalAuthSessionForRecovery(captureLocalAuthRecoveryScope()),
      { kind: 'ended', serverLogoutConfirmed: false }
    )
    const auth = useAuthStore.getState().auth
    assert.equal(auth.user, null)
    assert.equal(auth.session, null)
    assert.equal(auth.accessToken, null)
    assert.equal(auth.pending2FAFlowToken, null)
    assert.equal(auth.pending2FAFlowExpiresAt, null)
    assert.equal(auth.bootstrapState, 'complete')
    assert.equal(cache.getQueryCache().getAll().length, 0)
    assert.equal(storage.get(markerKey), 'pending:synthetic-crashed-writer')
    assert.equal(writes.length, beforeWrites)
    assert.deepEqual(published, [])
  } finally {
    unbind()
  }
})

test('storage deletion leaves the old realm fenced and late OAuth/direct bundle/MFA commits cannot resurrect it', async () => {
  storage.clear()
  assert.equal(getAuthCookieBlockedState(), 'uncertain')
  assert.throws(() => applyAuthBundle(bundle(), false), AuthCookieBlockedError)
  assert.throws(() => applyPending2FAFlow('late-mfa'), AuthCookieBlockedError)
  assert.equal(useAuthStore.getState().auth.user, null)
  assert.equal(useAuthStore.getState().auth.pending2FAFlowToken, null)
  let dispatches = 0
  setDevelopmentAuthRefreshAdapter(async () => {
    dispatches++
    throw new Error('must not refresh')
  })
  assert.deepEqual(await bootstrapAuthentication(), { kind: 'anonymous' })
  await assert.rejects(logout(), AuthCookieBlockedError)
  assert.equal(dispatches, 0)
})

test('a stale button cannot clear a newer account', () => {
  const stale = captureLocalAuthRecoveryScope()
  // Only a synthetic local-state mutation: production callbacks use the guarded commits.
  useAuthStore.getState().auth.setBundle(bundle(99, 'new-synthetic-session'))
  assert.equal(endLocalAuthSessionForRecovery(stale).kind, 'superseded')
  assert.equal(useAuthStore.getState().auth.user?.id, 99)
})

test('an older queued refresh rejection cannot revert explicit local completion to idle', async () => {
  const entered = deferred(),
    release = deferred()
  const holder = locks.request('foreign-writer', {}, async () => {
    entered.resolve()
    await release.promise
  })
  await entered.promise
  let dispatches = 0
  setDevelopmentAuthRefreshAdapter(async () => {
    dispatches++
    throw new Error('must not dispatch')
  })
  const refresh = refreshAuthentication()
  const result = endLocalAuthSessionForRecovery(captureLocalAuthRecoveryScope())
  assert.equal(result.kind, 'ended')
  release.resolve()
  await holder
  assert.equal((await refresh).kind, 'transient_error')
  assert.equal(useAuthStore.getState().auth.bootstrapState, 'complete')
  assert.equal(dispatches, 0)
  assert.deepEqual(await bootstrapAuthentication(), { kind: 'anonymous' })
})

test('foreign authentication events cannot reload or restore this ended tab; anonymous explicit exit stays local', async () => {
  let reloads = 0,
    signedOut = 0
  const reconciler = createAuthSessionReconciler({
    reload: () => {
      reloads++
    },
    signedOut: () => {
      signedOut++
    },
  })
  await reconciler.handle({
    kind: 'authenticated',
    sid: 'foreign-new-session',
    source: 'synthetic-other-tab',
    nonce: 'synthetic',
    timestamp: Date.now(),
  })
  assert.equal(reloads, 0)
  assert.equal(signedOut, 0)
  const result = endLocalAuthSessionForRecovery(captureLocalAuthRecoveryScope())
  assert.deepEqual(result, { kind: 'ended', serverLogoutConfirmed: false })
  assert.deepEqual(published, [])
  await assert.rejects(
    withAuthCookieLock(async () => {
      throw new Error('must remain fenced')
    }),
    AuthCookieBlockedError
  )
  reconciler.dispose()
})
