/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'
import type { ReactNode } from 'react'

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
const navigation: unknown[] = []
const successes: string[] = []
const errors: string[] = []
mock.module('@tanstack/react-router', () => ({
  useNavigate: () => (value: unknown) => {
    navigation.push(value)
  },
}))
mock.module('sonner', () => ({
  toast: {
    success: (value: string) => {
      successes.push(value)
    },
    error: (value: string) => {
      errors.push(value)
    },
  },
}))
// Only the outer dialog layout is replaced. Exercise the real sign-out handler,
// real Cookie preflight, local recovery component and authentication store.
mock.module('@/components/confirm-dialog', () => ({
  ConfirmDialog: ({
    handleConfirm,
    children,
    disabled,
    isLoading,
  }: {
    handleConfirm: () => Promise<void>
    children?: ReactNode
    disabled?: boolean
    isLoading?: boolean
  }) => (
    <div>
      <button
        type='button'
        disabled={disabled || isLoading}
        onClick={() => void handleConfirm()}
      >
        Sign out
      </button>
      {children}
    </div>
  ),
}))
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { SignOutDialog } = await import('./sign-out-dialog')
const { useAuthStore } = await import('@/stores/auth-store')
const { api } = await import('@/lib/api')
const i18n = createInstance()
await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
const originalAdapter = api.defaults.adapter
after(() => {
  api.defaults.adapter = originalAdapter
  mock.restore()
  dom.close()
})

test('blocked real logout offers a separate local action with zero HTTP, no success toast and no automatic navigation', async () => {
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
  const marker = 'uncertain:synthetic-unknown-cookie-response'
  window.localStorage.setItem('new-api:auth-cookie:uncertain', marker)
  let dispatches = 0,
    closed = 0
  api.defaults.adapter = async () => {
    dispatches++
    throw new Error('must not dispatch')
  }
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  try {
    await act(async () =>
      root.render(
        <I18nextProvider i18n={i18n}>
          <SignOutDialog
            open
            onOpenChange={(open) => {
              if (!open) closed++
            }}
          />
        </I18nextProvider>
      )
    )
    const normalButton = host.querySelector('button')
    assert.ok(normalButton)
    await act(async () => {
      normalButton.click()
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    assert.equal(dispatches, 0)
    assert.deepEqual(successes, [])
    assert.deepEqual(errors, [])
    assert.deepEqual(navigation, [])
    assert.equal(useAuthStore.getState().auth.user?.id, 42)
    assert.equal(normalButton.disabled, true)
    const localButton = Array.from(host.querySelectorAll('button')).find(
      (button) => button.textContent === "End this tab's local session"
    )
    assert.ok(localButton)
    await act(async () => localButton.click())
    assert.equal(useAuthStore.getState().auth.user, null)
    assert.equal(dispatches, 0)
    assert.equal(closed, 1)
    assert.deepEqual(successes, [])
    assert.deepEqual(navigation, [{ to: '/', replace: true }])
    assert.equal(
      window.localStorage.getItem('new-api:auth-cookie:uncertain'),
      marker
    )
  } finally {
    await act(async () => root.unmount())
  }
})
