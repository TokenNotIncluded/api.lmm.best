/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window({
  url: 'https://console.example.test/store/claim/PATH_SECRET?email=QUERY_SECRET#HASH_SECRET',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'MutationObserver',
  'getComputedStyle',
  'requestAnimationFrame',
  'cancelAnimationFrame',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
Object.defineProperty(globalThis, 'scrollTo', {
  configurable: true,
  value: () => {},
})
const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
  redirect,
} = await import('@tanstack/react-router')
const { CanceledError } = await import('axios')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { GeneralError } = await import('./general-error')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: {}, keySeparator: false })
const roots: ReturnType<typeof createRoot>[] = []
afterEach(async () => {
  for (const root of roots.splice(0)) await act(async () => root.unmount())
  document.body.replaceChildren()
  window.localStorage.clear()
})
after(() => dom.close())

async function mount(initialError: unknown) {
  let setError!: (error: unknown) => void
  const rootRoute = createRootRoute({ component: Outlet })
  const page = createRoute({
    getParentRoute: () => rootRoute,
    path: '/store/claim/$token',
    component: function DiagnosticFixture() {
      const [state, update] = useState({ error: initialError })
      setError = (value) => update({ error: value })
      return <GeneralError error={state.error} />
    },
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([page]),
    history: createMemoryHistory({
      initialEntries: [
        '/store/claim/PATH_SECRET?email=QUERY_SECRET#HASH_SECRET',
      ],
    }),
    defaultPendingMs: 0,
    defaultPendingMinMs: 0,
  })
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  roots.push(root)
  await act(async () => {
    await router.load()
    root.render(
      <I18nextProvider i18n={i18n}>
        <RouterProvider router={router} />
      </I18nextProvider>
    )
  })
  return {
    host,
    async update(error: unknown) {
      await act(async () => setError(error))
    },
  }
}

function copyButton(host: HTMLElement): HTMLButtonElement {
  const result = Array.from(host.querySelectorAll('button')).find(
    (button) => button.textContent === 'Copy diagnostic information'
  )
  assert.ok(result)
  return result
}
async function click(button: HTMLButtonElement) {
  await act(async () => {
    button.click()
    await Promise.resolve()
  })
}

test('real error page copies only safe fields, with stable per-error local ID and UTC', async () => {
  const copies: string[] = []
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: {
      async writeText(value: string) {
        copies.push(value)
      },
    },
  })
  window.localStorage.setItem('synthetic-secret', 'STORAGE_SECRET')
  const error = {
    response: { status: 500, data: { message: 'BODY_SECRET' } },
    message: 'MESSAGE_SECRET',
    stack: 'STACK_SECRET',
    config: { headers: { Authorization: 'TOKEN_SECRET' } },
  }
  const { host, update } = await mount(error)
  assert.equal(copies.length, 0, 'copy is never automatic')
  assert.ok(host.querySelector('[aria-label="500"]'))
  await click(copyButton(host))
  assert.match(host.textContent ?? '', /Diagnostic information copied/)
  await update(error)
  await click(copyButton(host))
  assert.equal(
    copies[0],
    copies[1],
    'rerender and repeated copy preserve the incident'
  )
  const record = JSON.parse(copies[0])
  assert.equal(record.route_template, '/store/claim/$token')
  assert.equal(record.http_status, 500)
  assert.match(record.local_event_id, /^[a-f0-9]{32}$/)
  assert.match(host.textContent ?? '', /not a server request ID/)
  assert.doesNotMatch(
    copies.join('') + host.textContent,
    /PATH_SECRET|QUERY_SECRET|HASH_SECRET|STORAGE_SECRET|BODY_SECRET|MESSAGE_SECRET|STACK_SECRET|TOKEN_SECRET/
  )
  await update(new Error('NEXT_SECRET'))
  await click(copyButton(host))
  assert.notEqual(JSON.parse(copies[2]).local_event_id, record.local_event_id)
  assert.equal(JSON.parse(copies[2]).http_status, null)
  assert.equal(host.querySelector('[aria-label="500"]'), null)
})

test('HTTP 200 business failure remains 200; clipboard false or throw gives fixed feedback', async () => {
  const copies: string[] = []
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: {
      async writeText(value: string) {
        copies.push(value)
        return false
      },
    },
  })
  const { host } = await mount({
    response: { status: 200, data: { success: false, message: 'SECRET' } },
  })
  await click(copyButton(host))
  assert.match(host.textContent ?? '', /Could not copy diagnostic information/)
  assert.equal(JSON.parse(copies[0]).http_status, 200)
  assert.equal(JSON.parse(copies[0]).kind, 'business_error')
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: {
      async writeText() {
        throw new Error('CLIPBOARD_SECRET')
      },
    },
  })
  await click(copyButton(host))
  assert.match(host.textContent ?? '', /Could not copy diagnostic information/)
  assert.doesNotMatch(host.textContent ?? '', /CLIPBOARD_SECRET/)
})

test('real cancellation and redirect objects render no general fault page', async () => {
  for (const error of [
    new CanceledError('SECRET'),
    new DOMException('SECRET', 'AbortError'),
    redirect({ to: '/sign-in' }),
  ]) {
    const { host } = await mount(error)
    assert.equal(host.querySelector('.error-editorial'), null)
    assert.equal(host.querySelector('button'), null)
  }
})

test('missing secure entropy disables copying with explicit feedback and no fallback', async () => {
  const original = globalThis.crypto
  Object.defineProperty(globalThis, 'crypto', {
    configurable: true,
    value: undefined,
  })
  try {
    const { host } = await mount(new Error('SECRET'))
    assert.equal(copyButton(host).disabled, true)
    assert.match(
      host.textContent ?? '',
      /Diagnostic information is unavailable in this browser/
    )
  } finally {
    Object.defineProperty(globalThis, 'crypto', {
      configurable: true,
      value: original,
    })
  }
})
