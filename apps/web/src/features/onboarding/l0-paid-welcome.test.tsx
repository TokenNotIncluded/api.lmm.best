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
const { L0Welcome } = await import('./l0-welcome')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
;(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true
const originalGet = api.get

after(() => {
  api.get = originalGet
  dom.close()
})

test('L0 top-up goes straight to wallet without opening an assistant or hiding pending review', async () => {
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
  api.get = (async () => ({
    data: { success: true, data: {} },
  })) as typeof api.get
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
        <p>Pending review</p>
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
      await new Promise((resolve) => setTimeout(resolve, 50))
    })
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 30))
    })
    assert.ok(container.textContent?.includes('Pending review'))
    const button = container.querySelector<HTMLButtonElement>(
      '[data-testid="l0-topup-direct"]'
    )
    assert.ok(button)
    assert.equal(button.disabled, false)
    assert.ok(container.querySelector('a[href="#l0-access-title"]'))
    await act(async () => {
      button.click()
      await new Promise((resolve) => setTimeout(resolve, 30))
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
