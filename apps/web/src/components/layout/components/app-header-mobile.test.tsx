/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({ url: 'https://console.example.test/' })
domWindow.document.write(
  '<!doctype html><html><head></head><body></body></html>'
)
Object.defineProperty(domWindow.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'history',
  'location',
  'HTMLElement',
  'HTMLButtonElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'PointerEvent',
  'FocusEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'scrollTo',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createRootRoute, createRouter, createMemoryHistory, RouterProvider } =
  await import('@tanstack/react-router')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { SidebarProvider } = await import('@/components/ui/sidebar')
const { api } = await import('@/lib/api')
const { formatQuota } = await import('@/lib/format')
const { useAuthStore } = await import('@/stores/auth-store')
const { consumeQueuedAssistantRequest, subscribeToAssistantOpen } =
  await import('@/features/assistant/assistant-events')
const { isAssistantRailOpen, setAssistantRailOpen } =
  await import('@/features/assistant/assistant-rail')
const { AppHeader } = await import('./app-header')

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})
const originalGet = api.get

afterEach(() => {
  api.get = originalGet
  useAuthStore.getState().auth.setUser(null)
  consumeQueuedAssistantRequest()
  setAssistantRailOpen(false)
  document.body.replaceChildren()
})
after(() => domWindow.close())

async function renderHeader(
  width: number,
  showAssistant = true,
  assistantEnabled = true,
  headerOptions: { showStore?: boolean; showNotifications?: boolean } = {}
) {
  Object.defineProperty(window, 'innerWidth', {
    configurable: true,
    value: width,
  })
  window.matchMedia = ((query: string) => ({
    matches: query.includes('min-width: 1280px') ? width >= 1280 : width < 768,
    media: query,
    onchange: null,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    addListener: () => undefined,
    removeListener: () => undefined,
    dispatchEvent: () => false,
  })) as typeof window.matchMedia
  api.get = (async (url: string) => ({
    data: {
      success: true,
      data:
        url === '/api/status'
          ? { assistant: { enabled: assistantEnabled } }
          : url === '/api/ratio-notifications'
            ? []
            : '',
    },
  })) as typeof api.get
  useAuthStore.getState().auth.setUser({
    id: 7,
    username: 'mobile-user',
    role: 1,
    quota: 125000000,
    developer_access_granted: true,
  })
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const route = createRootRoute({
    component: () => (
      <SidebarProvider>
        <AppHeader
          showBrand={false}
          showTopNav={false}
          showLanguageSwitcher={false}
          showConfigDrawer={false}
          showProfileDropdown={false}
          showNotifications={false}
          showAssistant={showAssistant}
          showMobileAssistant
          leftContent={<span>Users</span>}
          {...headerOptions}
        />
      </SidebarProvider>
    ),
  })
  const router = createRouter({
    routeTree: route,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <RouterProvider router={router} />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await new Promise((resolve) => setTimeout(resolve, 30))
  })
  await act(async () => new Promise((resolve) => setTimeout(resolve, 30)))
  return {
    container,
    cleanup: async () => {
      await act(async () => root.unmount())
      queryClient.clear()
    },
  }
}

test('the mobile header opens the same assistant request and keeps the wallet entry named', async () => {
  const requests: Array<{ autoSend: boolean }> = []
  const unsubscribe = subscribeToAssistantOpen((request) =>
    requests.push(request)
  )
  const rendered = await renderHeader(390)
  try {
    const assistant = rendered.container.querySelector<HTMLButtonElement>(
      '[data-testid="header-assistant-launcher"]'
    )
    assert.ok(assistant)
    assert.equal(assistant.getAttribute('aria-label'), 'Open AI assistant')
    await act(async () => assistant.click())
    assert.equal(requests.length, 1)
    assert.equal(requests[0]?.autoSend, false)
    const wallet = rendered.container.querySelector<HTMLAnchorElement>(
      '[data-testid="mobile-account-balance"]'
    )
    assert.ok(wallet)
    assert.equal(wallet.getAttribute('href'), '/wallet')
    assert.equal(
      wallet.getAttribute('aria-label'),
      `Balance: ${formatQuota(125000000)} · Top up`
    )
  } finally {
    unsubscribe()
    await rendered.cleanup()
  }
})

test('the desktop header keeps toggling its rail instead of queuing a mobile request', async () => {
  const requests: unknown[] = []
  const unsubscribe = subscribeToAssistantOpen((request) =>
    requests.push(request)
  )
  const rendered = await renderHeader(1440)
  try {
    const assistant = rendered.container.querySelector<HTMLButtonElement>(
      '[data-testid="header-assistant-launcher"]'
    )
    assert.ok(assistant)
    await act(async () => assistant.click())
    assert.equal(isAssistantRailOpen(), true)
    await act(async () => assistant.click())
    assert.equal(isAssistantRailOpen(), false)
    assert.equal(requests.length, 0)
  } finally {
    unsubscribe()
    await rendered.cleanup()
  }
})

test('signed-in mobile users keep the existing launcher entry when the public assistant flag is off', async () => {
  const rendered = await renderHeader(390, true, false)
  try {
    const assistant = rendered.container.querySelector<HTMLButtonElement>(
      '[data-testid="header-assistant-launcher"]'
    )
    assert.ok(assistant)
    await act(async () => assistant.click())
    assert.equal(consumeQueuedAssistantRequest()?.autoSend, false)
  } finally {
    await rendered.cleanup()
  }
})

test('a header that hides the assistant does not expose the compact entry', async () => {
  const rendered = await renderHeader(390, false)
  try {
    assert.equal(
      rendered.container.querySelector(
        '[data-testid="header-assistant-launcher"]'
      ),
      null
    )
  } finally {
    await rendered.cleanup()
  }
})

test('the shop link stays named and reachable when the assistant is hidden', async () => {
  const rendered = await renderHeader(390, false)
  try {
    const shop = rendered.container.querySelector<HTMLAnchorElement>(
      '[data-testid="header-store-link"]'
    )
    assert.ok(shop)
    assert.equal(shop.getAttribute('href'), '/store')
    assert.equal(shop.getAttribute('aria-label'), 'Open shop')
    assert.equal(shop.tagName, 'A')
  } finally {
    await rendered.cleanup()
  }
})

for (const width of [320, 640, 767]) {
  test(`the ${width}px header groups secondary controls without hiding the wallet`, async () => {
    const rendered = await renderHeader(width, true, true, {
      showNotifications: true,
    })
    try {
      const trigger = rendered.container.querySelector<HTMLButtonElement>(
        '[data-testid="header-more-actions"]'
      )
      assert.ok(trigger)
      assert.equal(trigger.getAttribute('aria-expanded'), 'false')
      assert.ok(
        rendered.container.querySelector(
          '[data-testid="mobile-account-balance"]'
        )
      )
      assert.equal(
        rendered.container.querySelector('[data-testid="header-store-link"]'),
        null
      )
      await act(async () => trigger.click())
      assert.equal(trigger.getAttribute('aria-expanded'), 'true')
      const menu = document.querySelector('[data-testid="header-tools-menu"]')
      assert.ok(menu)
      const shop = menu.querySelector<HTMLAnchorElement>(
        '[data-testid="header-store-link"]'
      )
      assert.ok(shop)
      assert.equal(shop.getAttribute('href'), '/store')
      assert.ok(menu.querySelector('[data-testid="header-tool-notifications"]'))
      // Test dismissal without issuing a request or requiring a store route fixture.
      shop.addEventListener('click', (event) => event.preventDefault())
      await act(async () => shop.click())
      assert.equal(trigger.getAttribute('aria-expanded'), 'false')
    } finally {
      await rendered.cleanup()
    }
  })
}

test('the desktop header leaves secondary controls directly available', async () => {
  const rendered = await renderHeader(1440, true, true, {
    showNotifications: true,
  })
  try {
    assert.equal(
      rendered.container.querySelector('[data-testid="header-more-actions"]'),
      null
    )
    assert.ok(
      rendered.container.querySelector('[data-testid="header-store-link"]')
    )
  } finally {
    await rendered.cleanup()
  }
})

test('disabled secondary controls do not create an empty more menu', async () => {
  const rendered = await renderHeader(390, true, true, { showStore: false })
  try {
    assert.equal(
      rendered.container.querySelector('[data-testid="header-more-actions"]'),
      null
    )
    assert.equal(
      rendered.container.querySelector('[data-testid="header-store-link"]'),
      null
    )
  } finally {
    await rendered.cleanup()
  }
})
