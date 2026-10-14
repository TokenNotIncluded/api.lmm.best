/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

import type { RefreshOutcome } from '@/lib/auth-session'

const dom = new Window({ url: 'https://console.example.test/profile' })
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
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} = await import('@tanstack/react-router')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { AuthBootstrapPendingError } =
  await import('@/lib/auth-bootstrap-navigation')
const { AuthBootstrapPending } = await import('./auth-bootstrap-pending')
const i18n = createInstance()
await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
const roots: ReturnType<typeof createRoot>[] = []
afterEach(async () => {
  for (const root of roots.splice(0)) await act(async () => root.unmount())
  document.body.replaceChildren()
})
after(() => dom.close())
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}
async function mount(beforeLoad: () => unknown) {
  const top = createRootRoute({
    component: Outlet,
    beforeLoad,
    errorComponent: ({ error }) => (
      <AuthBootstrapPending
        error={error as InstanceType<typeof AuthBootstrapPendingError>}
      />
    ),
  })
  const profile = createRoute({
    getParentRoute: () => top,
    path: '/profile',
    component: () => <p>Protected profile content</p>,
  })
  const home = createRoute({
    getParentRoute: () => top,
    path: '/',
    component: () => <p>Public home content</p>,
  })
  const router = createRouter({
    routeTree: top.addChildren([profile, home]),
    history: createMemoryHistory({ initialEntries: ['/profile'] }),
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
  return { router, host }
}
async function flush() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 25))
  })
}

test('the pending error view waits for its original task and only restores that current route after success', async () => {
  const task = deferred<RefreshOutcome>()
  const error = new AuthBootstrapPendingError(task.promise, '/profile')
  let settled = false,
    runs = 0
  const { router, host } = await mount(() => {
    runs++
    if (!settled) throw error
  })
  assert.match(host.textContent ?? '', /Loading/)
  assert.match(host.textContent ?? '', /Retry/)
  assert.doesNotMatch(
    host.textContent ?? '',
    /500|request fell over|Protected profile content/
  )
  settled = true
  await act(async () => {
    task.resolve({ kind: 'anonymous' })
    await task.promise
  })
  await flush()
  assert.match(host.textContent ?? '', /Protected profile content/)
  assert.equal(router.state.location.href, '/profile')
  assert.equal(runs, 2)
})

test('finishing an old task after leaving does not pull the user back to its protected page', async () => {
  const task = deferred<RefreshOutcome>()
  const error = new AuthBootstrapPendingError(task.promise, '/profile')
  let first = true
  const { router, host } = await mount(() => {
    if (first) {
      first = false
      throw error
    }
  })
  await act(async () => {
    await router.navigate({ to: '/' })
  })
  assert.match(host.textContent ?? '', /Public home/)
  await act(async () => {
    task.resolve({ kind: 'anonymous' })
    await task.promise
  })
  await flush()
  assert.equal(router.state.location.href, '/')
  assert.match(host.textContent ?? '', /Public home/)
})

test('same-URL replacement navigation remains in charge when an old task finishes during its pending phase', async () => {
  const task = deferred<RefreshOutcome>(),
    gate = deferred<void>()
  const error = new AuthBootstrapPendingError(task.promise, '/profile')
  let runs = 0
  const { router, host } = await mount(() => {
    runs++
    if (runs === 1) throw error
    if (runs === 2) return gate.promise
  })
  const original = router.invalidate.bind(router)
  let invalidations = 0
  router.invalidate = ((...args: Parameters<typeof router.invalidate>) => {
    invalidations++
    return original(...args)
  }) as typeof router.invalidate
  let navigation!: Promise<void>
  await act(async () => {
    navigation = router.navigate({ to: '/profile' })
    await new Promise((resolve) => setTimeout(resolve, 5))
  })
  assert.equal(router.state.isLoading, true)
  await act(async () => {
    task.resolve({ kind: 'anonymous' })
    await task.promise
  })
  await flush()
  assert.equal(invalidations, 0)
  await act(async () => {
    gate.resolve()
    await navigation
  })
  await flush()
  assert.match(host.textContent ?? '', /Protected profile content/)
  assert.equal(runs, 2)
})

test('a terminal transient check offers recovery without automatically dispatching another refresh', async () => {
  const task = deferred<RefreshOutcome>()
  const error = new AuthBootstrapPendingError(task.promise, '/profile')
  let runs = 0
  const { host } = await mount(() => {
    runs++
    throw error
  })
  await act(async () => {
    task.resolve({
      kind: 'transient_error',
      error: new Error('backend unavailable'),
    })
    await task.promise
  })
  await flush()
  assert.match(host.textContent ?? '', /request fell over/)
  assert.match(host.textContent ?? '', /Retry/)
  assert.equal(runs, 1)
})

test('explicit retry while the task is still pending waits on that same task', async () => {
  const task = deferred<RefreshOutcome>(),
    gate = deferred<void>()
  const error = new AuthBootstrapPendingError(task.promise, '/profile')
  let runs = 0
  const { host } = await mount(() => {
    runs++
    if (runs === 1) throw error
    return gate.promise
  })
  const retry = [...host.querySelectorAll('button')].find(
    (button) => button.textContent === 'Retry'
  )
  assert.ok(retry)
  await act(async () => {
    retry.click()
    await new Promise((resolve) => setTimeout(resolve, 5))
  })
  assert.equal(runs, 2)
  await act(async () => {
    task.resolve({ kind: 'anonymous' })
    gate.resolve()
    await task.promise
  })
  await flush()
  assert.match(host.textContent ?? '', /Protected profile content/)
  assert.equal(runs, 2)
})
