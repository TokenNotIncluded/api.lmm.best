/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window({ url: 'https://console.example.test/' })
for (const key of [
  'window',
  'document',
  'navigator',
  'history',
  'location',
  'HTMLElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'scrollTo',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.defineProperty(dom.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
const media = () => ({
  matches: true,
  media: '',
  addListener() {},
  removeListener() {},
  addEventListener() {},
  removeEventListener() {},
  dispatchEvent() {
    return false
  },
})
Object.defineProperty(dom, 'matchMedia', { configurable: true, value: media })
Object.defineProperty(globalThis, 'matchMedia', {
  configurable: true,
  value: media,
})
Object.defineProperty(globalThis, 'customElements', {
  configurable: true,
  value: {
    get() {
      return undefined
    },
    define() {},
  },
})
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
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
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { subscribeToAssistantOpen } =
  await import('@/features/assistant/assistant-events')
const { developerAccessRequestQueryKey } = await import('./api')
const { L0Welcome } = await import('./l0-welcome')
const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})
const globals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
globals.IS_REACT_ACT_ENVIRONMENT = true
const originalGet = api.get
const flush = () => new Promise((resolve) => setTimeout(resolve, 30))

after(() => {
  api.get = originalGet
  dom.close()
})

test('compact L0 preserves status and goes directly to checkout', async () => {
  const user = {
    id: 707,
    username: 'paid-onboarding-test',
    role: 1,
    developer_access_granted: false,
    onboarding: {
      activation_complete: false,
      credential_complete: false,
      first_request_complete: false,
      stage: 'activate' as const,
      paid_activation_enabled: true,
      paid_activation_min_amount: 1,
    },
  }
  let accessReads = 0
  api.get = (async (url) => {
    if (url === '/api/user/developer-access/request') accessReads++
    return { data: { success: true, data: {} } }
  }) as typeof api.get
  useAuthStore.getState().auth.setUser(user)
  const opened: Array<string | undefined> = []
  const unsubscribe = subscribeToAssistantOpen((event) =>
    opened.push(event.preset)
  )
  const rootRoute = createRootRoute({ component: Outlet })
  const welcome = createRoute({
    getParentRoute: () => rootRoute,
    path: '/getting-started',
    component: () => (
      <L0Welcome user={user}>
        <p>Pending application details</p>
      </L0Welcome>
    ),
  })
  const wallet = createRoute({
    getParentRoute: () => rootRoute,
    path: '/wallet',
    component: () => <p>Checkout</p>,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([welcome, wallet]),
    history: createMemoryHistory({ initialEntries: ['/getting-started'] }),
  })
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  queryClient.setQueryData(developerAccessRequestQueryKey(user.id), {
    status: 'pending',
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  try {
    await act(async () => {
      root.render(
        <QueryClientProvider client={queryClient}>
          <I18nextProvider i18n={i18n}>
            <RouterProvider router={router} />
          </I18nextProvider>
        </QueryClientProvider>
      )
      await flush()
    })
    await act(flush)
    const details = container.querySelector<HTMLDetailsElement>(
      '[data-testid="l0-account-details"]'
    )
    assert.ok(details)
    assert.equal(details.open, false)
    assert.match(details.querySelector('summary')?.textContent ?? '', /Pending/)
    assert.match(details.textContent ?? '', /Pending application details/)
    assert.equal(accessReads, 0)
    await act(async () => {
      queryClient.setQueryData(developerAccessRequestQueryKey(user.id), {
        status: 'rejected',
      })
      await flush()
    })
    assert.match(
      details.querySelector('summary')?.textContent ?? '',
      /Access request rejected/
    )
    assert.equal(accessReads, 0)
    const button = container.querySelector<HTMLButtonElement>(
      '[data-testid="l0-topup-direct"]'
    )
    assert.ok(button)
    assert.equal(button.disabled, false)
    await act(async () => {
      button.click()
      await flush()
    })
    assert.equal(router.state.location.pathname, '/wallet')
    assert.deepEqual(opened, [])
  } finally {
    await act(async () => root.unmount())
    queryClient.clear()
    unsubscribe()
    container.remove()
    api.get = originalGet
  }
})
