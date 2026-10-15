/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// Regression derived from the independent late-language fixture; author check only.
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { Window } from 'happy-dom'

import type { AuthBundle } from '@/stores/auth-store'

const dom = new Window({ url: 'https://console.example.test/sign-in' })
for (const key of [
  'window',
  'document',
  'navigator',
  'localStorage',
  'sessionStorage',
  'HTMLElement',
  'HTMLInputElement',
  'Element',
  'SVGElement',
  'Node',
  'Event',
  'MouseEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'matchMedia',
]) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: Reflect.get(dom, key),
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const navigations: unknown[] = []
mock.module('@tanstack/react-router', () => ({
  useNavigate: () => async (options: unknown) => {
    navigations.push(options)
  },
}))
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const i18n = (await import('i18next')).default
const { applyAuthBundle, clearAuthentication } =
  await import('@/lib/auth-session')
const { useAuthStore } = await import('@/stores/auth-store')
const { useAuthRedirect } = await import('./use-auth-redirect')
const frenchCallbacks: Array<
  (error: null, value: Record<string, string>) => void
> = []
const reads: string[] = []
const releaseFrench = () => {
  for (const callback of frenchCallbacks.splice(0)) {
    callback(null, { hello: 'Bonjour' })
  }
}
let entered!: () => void
const frenchStarted = new Promise<void>((done) => {
  entered = done
})
const backend = {
  type: 'backend' as const,
  init() {},
  read(
    language: string,
    namespace: string,
    callback: (error: null, value: Record<string, string>) => void
  ) {
    reads.push(`${language}|${namespace}`)
    if (language === 'fr') {
      frenchCallbacks.push(callback)
      entered()
    } else callback(null, { hello: 'Hello' })
  },
}
await i18n.use(backend).init({
  lng: 'en',
  fallbackLng: 'de',
  ns: ['translation', 'profile'],
  defaultNS: 'translation',
})
let redirect!: ReturnType<typeof useAuthRedirect>
function Probe() {
  redirect = useAuthRedirect()
  return null
}
function bundle(token: string, id: number, language: string): AuthBundle {
  return {
    access_token: token,
    token_type: 'Bearer',
    access_expires_at: Math.floor(Date.now() / 1000) + 600,
    user: { id, username: 'synthetic', role: 1, language },
    session: {
      sid: `sid-${id}`,
      current: true,
      login_method: 'password',
      ip: '',
      user_agent: '',
      created_at: 1,
      last_active_at: 1,
      expires_at: 2_000_000_000,
    },
  }
}
test('cancelled account A language load must not replace account B language', async () => {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(<Probe />))
  const controller = new AbortController()
  const a = bundle('language-A', 41, 'fr')
  const b = bundle('language-B', 42, 'en')
  applyAuthBundle(a, false)
  const old = redirect.handleLoginSuccess(a, '/store', true, controller.signal)
  try {
    await frenchStarted
    assert.equal(i18n.language, 'en')
    controller.abort()
    applyAuthBundle(b, false)
    await redirect.handleLoginSuccess(b, '/store', true)
    assert.equal(navigations.length, 1)
    assert.equal(useAuthStore.getState().auth.accessToken, 'language-B')
    assert.equal(frenchCallbacks.length, 2)
    releaseFrench()
    await old
    assert.equal(navigations.length, 1, 'late A navigation remains suppressed')
    assert.equal(
      i18n.language,
      'en',
      'late cancelled account A resource completion changed account B global language'
    )
    assert.ok(i18n.hasResourceBundle('de', 'translation'))
    assert.ok(i18n.hasResourceBundle('de', 'profile'))
    const readsBeforeCommit = reads.length
    const current = bundle('language-C', 43, 'fr')
    applyAuthBundle(current, false)
    await redirect.handleLoginSuccess(current, '/store', true)
    assert.equal(i18n.language, 'fr')
    assert.equal(
      reads.length,
      readsBeforeCommit,
      'all namespaces and fallback resources must be ready before global language commit'
    )
  } finally {
    releaseFrench()
    await old
    await i18n.changeLanguage('en')
    clearAuthentication(false, 'idle')
    await act(async () => root.unmount())
    mock.restore()
    dom.close()
  }
})
