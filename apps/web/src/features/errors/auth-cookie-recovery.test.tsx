/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

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
mock.module('@tanstack/react-router', () => ({
  useNavigate: () => () => undefined,
}))
mock.module('./error-page-frame', () => ({
  ErrorPageFrame: () => <p>Generic 500 Retry</p>,
}))
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const {
  AuthCookieBlockedError,
  authCookieRecoveryMessage,
  withAuthCookieLock,
} = await import('@/lib/auth-cookie')
const { GeneralError } = await import('./general-error')
const i18n = createInstance()
await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
after(() => {
  mock.restore()
  dom.close()
})

test('protected-route error UI shows actionable site-data recovery instead of generic500 and Retry while preserving pending marker', async () => {
  const marker = 'pending:crashed-document'
  window.localStorage.setItem('new-api:auth-cookie:uncertain', marker)
  await assert.rejects(
    withAuthCookieLock(async () => {}),
    AuthCookieBlockedError
  )
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () =>
      root.render(
        <I18nextProvider i18n={i18n}>
          <GeneralError
            error={
              new Error('Authentication could not be checked. Please retry.')
            }
          />
        </I18nextProvider>
      )
    )
    assert.ok(host.querySelector('[role="alert"]'))
    assert.ok(host.textContent?.includes(authCookieRecoveryMessage))
    assert.doesNotMatch(host.textContent ?? '', /Generic 500|Retry/)
    assert.equal(
      window.localStorage.getItem('new-api:auth-cookie:uncertain'),
      marker
    )
  } finally {
    await act(async () => root.unmount())
  }
})
