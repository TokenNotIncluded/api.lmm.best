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

const domWindow = new Window({ url: 'https://console.example.test/' })
domWindow.document.write(
  '<!doctype html><html><head></head><body></body></html>'
)
Object.defineProperty(domWindow.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
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
for (const key of [
  'window',
  'document',
  'navigator',
  'history',
  'location',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'ResizeObserver',
  'getComputedStyle',
  'scrollTo',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'PointerEvent',
  'FocusEvent',
  'CustomEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

Object.defineProperty(window, 'matchMedia', {
  configurable: true,
  value: () => ({
    addEventListener: () => undefined,
    matches: true,
    removeEventListener: () => undefined,
  }),
})
Object.defineProperty(globalThis, 'matchMedia', {
  configurable: true,
  value: window.matchMedia,
})

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
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
const { DirectionProvider } = await import('@/context/direction-provider')
const { ThemeProvider } = await import('@/context/theme-provider')
const { ThemeCustomizationProvider } =
  await import('@/context/theme-customization-provider')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { requestAssistantOpen, consumeQueuedAssistantRequest } =
  await import('@/features/assistant/assistant-events')
const { setAssistantRailOpen } =
  await import('@/features/assistant/assistant-rail')
const { AuthenticatedLayout } = await import('./authenticated-layout')

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
after(() => domWindow.close())

test('L0 renders the wallet header and no sidebar assistant even when a previous rail was open', async () => {
  const originalGet = api.get
  const calls: string[] = []
  api.get = (async (url) => {
    calls.push(String(url))
    return {
      data: {
        success: true,
        data:
          url === '/api/status'
            ? { assistant: { enabled: true } }
            : url === '/api/user/self/announcements'
              ? []
              : null,
      },
    }
  }) as typeof api.get
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'l0',
    role: 1,
    quota: 500000,
    developer_access_granted: false,
  })
  setAssistantRailOpen(true)
  requestAssistantOpen('onboarding')
  const rootRoute = createRootRoute({ component: Outlet })
  const route = createRoute({
    getParentRoute: () => rootRoute,
    path: '/getting-started',
    component: () => (
      <AuthenticatedLayout>
        <main data-testid='l0-inline-content'>Inline conversation</main>
      </AuthenticatedLayout>
    ),
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([route]),
    history: createMemoryHistory({ initialEntries: ['/getting-started'] }),
  })
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const flush = () => new Promise((resolve) => setTimeout(resolve, 30))
  try {
    await act(async () => {
      root.render(
        <QueryClientProvider client={queryClient}>
          <I18nextProvider i18n={i18n}>
            <ThemeProvider>
              <ThemeCustomizationProvider>
                <DirectionProvider>
                  <RouterProvider router={router} />
                </DirectionProvider>
              </ThemeCustomizationProvider>
            </ThemeProvider>
          </I18nextProvider>
        </QueryClientProvider>
      )
      await flush()
    })
    await act(flush)
    assert.ok(container.querySelector('[data-testid="l0-inline-content"]'))
    const wallet = container.querySelector<HTMLElement>(
      '[data-testid="account-balance-badge"]'
    )
    const walletLinks =
      wallet?.querySelectorAll<HTMLAnchorElement>('a[href="/wallet"]')
    assert.equal(walletLinks?.length, 2)
    assert.match(wallet?.textContent ?? '', /Top up/)
    assert.equal(container.querySelector('[data-sidebar="trigger"]'), null)
    assert.equal(
      container.querySelector('button[aria-label="Open AI assistant"]'),
      null
    )
    assert.equal(
      container.querySelector(
        '[data-testid="assistant-rail"], [data-testid="assistant-mobile-launcher"], #ai-assistant-panel'
      ),
      null
    )
    await act(async () => {
      window.dispatchEvent(
        new domWindow.KeyboardEvent('keydown', {
          key: 'a',
          ctrlKey: true,
          shiftKey: true,
        }) as unknown as Event
      )
      await flush()
    })
    assert.equal(
      container.querySelector(
        '[data-testid="assistant-rail"], [data-testid="assistant-mobile-launcher"], #ai-assistant-panel'
      ),
      null
    )
    assert.equal(
      calls.some((url) => url.includes('/assistant/')),
      false
    )
  } finally {
    await act(async () => root.unmount())
    queryClient.clear()
    consumeQueuedAssistantRequest()
    setAssistantRailOpen(false)
    useAuthStore.getState().auth.setUser(null)
    api.get = originalGet
    container.remove()
  }
})
