/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// Regression derived from the independent production locale-alias fixture; author check only.
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
let releaseFrench:
  | ((error: null, value: Record<string, string>) => void)
  | undefined
let entered!: () => void
const frenchStarted = new Promise<void>((done) => {
  entered = done
})
const backend = {
  type: 'backend' as const,
  init() {},
  read(
    language: string,
    _namespace: string,
    callback: (error: null, value: Record<string, string>) => void
  ) {
    if (language === 'fr') {
      releaseFrench = callback
      entered()
    } else callback(null, { hello: 'Hello' })
  },
}
await i18n.use(backend).init({
  lng: 'en',
  fallbackLng: 'en',
  supportedLngs: ['en', 'zhCN', 'fr', 'ru', 'ja', 'vi', 'zhTW'],
  load: 'currentOnly',
  ns: ['translation'],
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
test('cancelled alias-locale account A must not replace account B language under production supportedLngs', async () => {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(<Probe />))
  const controller = new AbortController()
  const a = bundle('language-A', 41, 'fr-FR')
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
    assert.ok(releaseFrench)
    releaseFrench(null, { hello: 'Bonjour' })
    await old
    assert.equal(navigations.length, 1, 'late A navigation remains suppressed')
    assert.equal(
      i18n.language,
      'en',
      'late cancelled account A resource completion changed account B global language'
    )
  } finally {
    releaseFrench?.(null, { hello: 'Bonjour' })
    await old
    await i18n.changeLanguage('en')
    clearAuthentication(false, 'idle')
    await act(async () => root.unmount())
    mock.restore()
    dom.close()
  }
})
