/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { after, test } from 'node:test'

import type { AxiosResponse } from 'axios'

import { refreshCurrentAccount } from '@/features/onboarding/use-auth-user-refresh'
import { api, getSelf } from '@/lib/api'
import {
  captureLocalAuthRecoveryScope,
  endLocalAuthSessionForRecovery,
} from '@/lib/auth-session'
import {
  useAuthStore,
  type AuthBundle,
  type AuthUser,
} from '@/stores/auth-store'

// Execute this actual consumer's unchanged body against the real HTTP/store
// pipeline. Only unrelated query invalidation is an isolated test sink.
const source = readFileSync(new URL('./index.tsx', import.meta.url), 'utf8')
const body = source.match(
  / {2}const refresh = async \(balanceChanged = false\) => \{([\s\S]*?)\n {2}\}\n\n {2}const errorMessage/
)?.[1]
assert.ok(
  body,
  'bind the actual bounty refresh body; never substitute a success-only copy'
)
const refresh = new Function(
  'getSelf',
  'setUser',
  'refreshCurrentAccount',
  'queryClient',
  'BOUNTY_QUERY_KEYS',
  `return async (balanceChanged = false) => {${body}}`
)(
  getSelf,
  (user: AuthUser) => useAuthStore.getState().auth.setUser(user),
  refreshCurrentAccount,
  { invalidateQueries: async () => undefined },
  [['synthetic-bounty']]
) as (balanceChanged?: boolean) => Promise<void>
const storage = new Map<string, string>()
const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
Object.defineProperty(globalThis, 'window', {
  configurable: true,
  value: {
    localStorage: { getItem: (key: string) => storage.get(key) ?? null },
  },
})
const originalAdapter = api.defaults.adapter
const bundle = (id = 42): AuthBundle => ({
  access_token: `synthetic-local-${id}`,
  token_type: 'Bearer',
  access_expires_at: 2_000_000_000,
  user: { id, username: `synthetic-${id}`, role: 1 },
  session: {
    sid: `synthetic-session-${id}`,
    current: true,
    login_method: 'password',
    ip: '',
    user_agent: '',
    created_at: 1,
    last_active_at: 1,
    expires_at: 2_000_000_000,
  },
})
function deferredSelf() {
  let entered!: () => void
  const started = new Promise<void>((resolve) => {
    entered = resolve
  })
  let finish!: (username: string) => void
  api.defaults.adapter = async (config) => {
    assert.equal(config.url, '/api/user/self')
    return new Promise<AxiosResponse>((resolve) => {
      finish = (username) =>
        resolve({
          config,
          status: 200,
          statusText: 'synthetic',
          headers: {},
          data: { success: true, data: { ...bundle().user, username } },
        })
      entered()
    })
  }
  return { started, finish: (username: string) => finish(username) }
}
after(() => {
  api.defaults.adapter = originalAdapter
  if (originalWindow) {
    Object.defineProperty(globalThis, 'window', originalWindow)
  } else {
    Reflect.deleteProperty(globalThis, 'window')
  }
})

test('actual bounty balance refresh still updates the same authenticated account', async () => {
  useAuthStore.getState().auth.setBundle(bundle())
  const task = deferredSelf(),
    result = refresh(true)
  await task.started
  task.finish('synthetic-updated')
  await result
  assert.equal(useAuthStore.getState().auth.user?.username, 'synthetic-updated')
  assert.equal(
    useAuthStore.getState().auth.session?.sid,
    'synthetic-session-42'
  )
})

test('actual deferred bounty self response cannot restore user information after local-only exit', async () => {
  useAuthStore.getState().auth.setBundle(bundle())
  const task = deferredSelf(),
    result = refresh(true)
  await task.started
  storage.set('new-api:auth-cookie:uncertain', 'uncertain:synthetic-old-cookie')
  assert.equal(
    endLocalAuthSessionForRecovery(captureLocalAuthRecoveryScope()).kind,
    'ended'
  )
  task.finish('synthetic-old-response')
  await result
  const auth = useAuthStore.getState().auth
  assert.equal(auth.user, null)
  assert.equal(auth.session, null)
  assert.equal(auth.accessToken, null)
  assert.equal(auth.bootstrapState, 'complete')
  assert.equal(
    storage.get('new-api:auth-cookie:uncertain'),
    'uncertain:synthetic-old-cookie'
  )
})

test('actual old bounty response cannot replace a newer account', async () => {
  // Only synthetic local fixture seeding, not a server authentication proof.
  useAuthStore.getState().auth.setBundle(bundle())
  const task = deferredSelf(),
    result = refresh(true)
  await task.started
  useAuthStore.getState().auth.setBundle(bundle(99))
  task.finish('synthetic-old-account')
  await result
  assert.equal(useAuthStore.getState().auth.user?.id, 99)
  assert.equal(
    useAuthStore.getState().auth.session?.sid,
    'synthetic-session-99'
  )
  assert.equal(useAuthStore.getState().auth.accessToken, 'synthetic-local-99')
})

test('bounty refresh after local exit remains anonymous and dispatches no self request', async () => {
  assert.equal(
    endLocalAuthSessionForRecovery(captureLocalAuthRecoveryScope()).kind,
    'ended'
  )
  let dispatches = 0
  api.defaults.adapter = async () => {
    dispatches++
    throw new Error('anonymous recovery must not fetch old Cookie account')
  }
  await refresh(true)
  assert.equal(dispatches, 0)
  assert.equal(useAuthStore.getState().auth.user, null)
  assert.equal(useAuthStore.getState().auth.bootstrapState, 'complete')
})
