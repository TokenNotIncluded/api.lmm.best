/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import type { AxiosAdapter, AxiosResponse } from 'axios'
import { Window } from 'happy-dom'

const dom = new Window({ url: 'https://console.example.test/oauth/synthetic' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Node',
  'Element',
  'Event',
  'MutationObserver',
  'getComputedStyle',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const navigation: Array<{ to?: string; href?: string }> = []
const navigate = (value: { to?: string; href?: string }) => {
  navigation.push(value)
}
let search = { code: 'healthy-synthetic-code', state: 'synthetic-state' }
const realRouter = await import('@tanstack/react-router')
mock.module('@tanstack/react-router', () => ({
  ...realRouter,
  useNavigate: () => navigate,
  useParams: () => ({ provider: 'synthetic' }),
  useSearch: () => search,
}))
// Replace only presentation and feedback sinks; the real OAuth effect, HTTP
// adapter, redirect hook, MFA commit and auth generations remain exercised.
mock.module('@/features/auth/components/oauth-callback-screen', () => ({
  OAuthCallbackScreen: () => <div>Callback</div>,
}))
mock.module('sonner', () => ({
  toast: {
    error: () => undefined,
    success: () => undefined,
    info: () => undefined,
  },
}))
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { Route } = await import('./$provider')
const { api } = await import('@/lib/api')
const {
  applyAuthBundle,
  captureLocalAuthRecoveryScope,
  endLocalAuthSessionForRecovery,
} = await import('@/lib/auth-session')
const { useAuthStore } = await import('@/stores/auth-store')
const originalAdapter = api.defaults.adapter
const Callback = Route.options.component!
const bundle = {
  access_token: 'synthetic-local-state',
  token_type: 'Bearer',
  access_expires_at: 2_000_000_000,
  user: { id: 42, username: 'synthetic', role: 1 },
  session: {
    sid: 'synthetic-sid',
    current: true,
    login_method: 'password',
    ip: '',
    user_agent: '',
    created_at: 1,
    last_active_at: 1,
    expires_at: 2_000_000_000,
  },
}
let dispatches = 0
function deferredCallback() {
  let entered!: () => void
  const started = new Promise<void>((resolve) => {
    entered = resolve
  })
  let resolve!: (response: AxiosResponse) => void
  let requestConfig!: Parameters<AxiosAdapter>[0]
  api.defaults.adapter = async (config) => {
    assert.equal(config.url, '/api/oauth/synthetic')
    dispatches++
    requestConfig = config
    entered()
    return new Promise<AxiosResponse>((done) => {
      resolve = done
    })
  }
  return {
    started,
    finish(flow: string) {
      resolve({
        config: requestConfig,
        status: 200,
        statusText: 'synthetic',
        headers: {},
        data: {
          success: true,
          data: {
            require_2fa: true,
            flow_token: flow,
            expires_at: 2_000_000_000,
          },
        },
      })
    },
  }
}
after(() => {
  api.defaults.adapter = originalAdapter
  mock.restore()
  dom.close()
})

test('real healthy OAuth MFA callback advances authEpoch and supersedes an earlier same-account local recovery control', async () => {
  applyAuthBundle(bundle, false)
  const before = captureLocalAuthRecoveryScope()
  const task = deferredCallback()
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () => root.render(<Callback />))
    await task.started
    await act(async () => {
      task.finish('synthetic-healthy-mfa')
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    assert.equal(
      useAuthStore.getState().auth.pending2FAFlowToken,
      'synthetic-healthy-mfa'
    )
    const current = captureLocalAuthRecoveryScope()
    assert.equal(current.accountWriteEpoch, before.accountWriteEpoch)
    assert.equal(current.authEpoch, before.authEpoch + 1)
    assert.equal(endLocalAuthSessionForRecovery(before).kind, 'superseded')
    assert.equal(navigation.filter((entry) => entry.to === '/otp').length, 1)
  } finally {
    await act(async () => root.unmount())
  }
})

test('real deferred OAuth MFA response cannot restore a flow or navigate to OTP after explicit local-only exit', async () => {
  applyAuthBundle(bundle, false)
  search = { code: 'late-synthetic-code', state: 'synthetic-state' }
  const task = deferredCallback()
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  const beforeOtp = navigation.filter((entry) => entry.to === '/otp').length
  try {
    await act(async () => root.render(<Callback />))
    await task.started
    window.localStorage.setItem(
      'new-api:auth-cookie:uncertain',
      'uncertain:synthetic-late-writer'
    )
    assert.deepEqual(
      endLocalAuthSessionForRecovery(captureLocalAuthRecoveryScope()),
      { kind: 'ended', serverLogoutConfirmed: false }
    )
    await act(async () => {
      task.finish('synthetic-late-mfa')
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    assert.equal(useAuthStore.getState().auth.pending2FAFlowToken, null)
    assert.equal(useAuthStore.getState().auth.user, null)
    assert.equal(useAuthStore.getState().auth.bootstrapState, 'complete')
    assert.equal(
      navigation.filter((entry) => entry.to === '/otp').length,
      beforeOtp
    )
    assert.equal(
      dispatches,
      2,
      'only the two original synthetic callback GETs; no logout, refresh or replay'
    )
    assert.equal(
      window.localStorage.getItem('new-api:auth-cookie:uncertain'),
      'uncertain:synthetic-late-writer'
    )
  } finally {
    await act(async () => root.unmount())
  }
})
