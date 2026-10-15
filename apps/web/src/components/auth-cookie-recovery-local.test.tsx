/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { QueryClient } from '@tanstack/react-query'
import { Window } from 'happy-dom'

const dom = new Window({ url: 'https://console.example.test/profile' })
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
const navigations: unknown[] = []
mock.module('@tanstack/react-router', () => ({
  useNavigate: () => (value: unknown) => {
    navigations.push(value)
  },
}))
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { AuthCookieRecovery } = await import('./auth-cookie-recovery')
const { bindAuthCache, captureLocalAuthRecoveryScope } =
  await import('@/lib/auth-session')
const { useAuthStore } = await import('@/stores/auth-store')
const i18n = createInstance()
await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
after(() => {
  mock.restore()
  dom.close()
})

test('explicit recovery ends only this tab, clears private state and navigates to public home without unlocking Cookies', async () => {
  // Synthetic existing local state, not a server-issued authentication proof.
  useAuthStore.getState().auth.setBundle({
    access_token: 'synthetic-local-only',
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
  })
  useAuthStore.getState().auth.setPending2FAFlowToken('synthetic-mfa')
  const cache = new QueryClient()
  cache.setQueryData(['private'], 'synthetic-private-cache')
  const unbind = bindAuthCache(cache)
  const marker = 'uncertain:synthetic-unknown-response'
  window.localStorage.setItem('new-api:auth-cookie:uncertain', marker)
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () =>
      root.render(
        <I18nextProvider i18n={i18n}>
          <AuthCookieRecovery />
        </I18nextProvider>
      )
    )
    const button = Array.from(host.querySelectorAll('button')).find(
      (node) => node.textContent === "End this tab's local session"
    )
    assert.ok(
      button,
      'Cookie recovery needs an explicit local-only exit button'
    )
    assert.match(host.textContent ?? '', /Server sign-out is not confirmed/)
    await act(async () => button.click())
    const auth = useAuthStore.getState().auth
    assert.equal(auth.user, null)
    assert.equal(auth.accessToken, null)
    assert.equal(auth.session, null)
    assert.equal(auth.pending2FAFlowToken, null)
    assert.equal(auth.bootstrapState, 'complete')
    assert.equal(cache.getQueryCache().getAll().length, 0)
    assert.equal(
      window.localStorage.getItem('new-api:auth-cookie:uncertain'),
      marker
    )
    assert.deepEqual(navigations, [{ to: '/', replace: true }])
    assert.match(
      host.querySelector('[role="status"]')?.textContent ?? '',
      /local session has ended.*Server sign-out is not confirmed/
    )
    assert.doesNotMatch(host.textContent ?? '', /Signed out/)
  } finally {
    await act(async () => root.unmount())
    unbind()
  }
})

test('a recovery control bound to an older displayed account cannot clear a newer account', async () => {
  const stale = captureLocalAuthRecoveryScope()
  useAuthStore.getState().auth.setBundle({
    access_token: 'new-synthetic-local-only',
    token_type: 'Bearer',
    access_expires_at: 2_000_000_000,
    user: { id: 99, username: 'new-synthetic', role: 1 },
    session: {
      sid: 'new-synthetic-sid',
      current: true,
      login_method: 'password',
      ip: '',
      user_agent: '',
      created_at: 1,
      last_active_at: 1,
      expires_at: 2_000_000_000,
    },
  })
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  const beforeNavigation = navigations.length
  try {
    await act(async () =>
      root.render(
        <I18nextProvider i18n={i18n}>
          <AuthCookieRecovery expectedScope={stale} />
        </I18nextProvider>
      )
    )
    const button = host.querySelector('button')
    assert.ok(button)
    await act(async () => button.click())
    assert.equal(useAuthStore.getState().auth.user?.id, 99)
    assert.equal(navigations.length, beforeNavigation)
    assert.match(
      host.querySelector('[role="status"]')?.textContent ?? '',
      /session changed/
    )
    assert.doesNotMatch(host.textContent ?? '', /local session has ended/)
  } finally {
    await act(async () => root.unmount())
  }
})
