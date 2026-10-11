/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */

import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window({ url: 'http://localhost/' })
for (const key of [
  'window',
  'document',
  'navigator',
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
    value: dom[key],
  })
}
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
})
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createRootRoute, createRouter, createMemoryHistory, RouterProvider } =
  await import('@tanstack/react-router')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { SidebarProvider } = await import('@/components/ui/sidebar')
const { useAuthStore } = await import('@/stores/auth-store')
const { DEFAULT_CURRENCY_CONFIG, useSystemConfigStore } =
  await import('@/stores/system-config-store')
const { formatQuota } = await import('@/lib/format')
const { AccountBalanceBadge } = await import('./account-balance-badge')
const { ConsoleBalanceEntry } = await import('./console-navigation')
const originalUser = useAuthStore.getState().auth.user
const originalConfig = useSystemConfigStore.getState().config
const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})
let dispose: (() => Promise<void>) | undefined

afterEach(async () => {
  await dispose?.()
  dispose = undefined
  useAuthStore.getState().auth.setUser(originalUser)
  useSystemConfigStore.setState({ config: originalConfig })
  document.body.replaceChildren()
})
after(() => dom.close())

async function mount(projection: boolean | undefined, quota: number) {
  useSystemConfigStore.setState({
    config: {
      ...originalConfig,
      currency: {
        ...DEFAULT_CURRENCY_CONFIG,
        currencyUnit: 'credit',
        creditsPerUsd: 500000,
        creditsPerUsdExact: '500000',
      },
    },
  })
  useAuthStore.getState().auth.setUser({
    id: 905,
    username: 'synthetic-balance-user',
    role: 1,
    quota,
    financial_projection_available: projection,
    usage_projection_available: false,
    setting: JSON.stringify({ wallet_display_currency: 'USD' }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const route = createRootRoute({
    component: () => (
      <SidebarProvider>
        <AccountBalanceBadge compactMobile />
        <div data-testid='console-balance'>
          <ConsoleBalanceEntry />
        </div>
      </SidebarProvider>
    ),
  })
  const router = createRouter({
    routeTree: route,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () =>
    root.render(
      <QueryClientProvider client={client}>
        <I18nextProvider i18n={i18n}>
          <RouterProvider router={router} />
        </I18nextProvider>
      </QueryClientProvider>
    )
  )
  dispose = async () => {
    await act(async () => root.unmount())
    client.clear()
  }
  return container
}

for (const quota of [0, 5_000_000]) {
  test(`unavailable financial projection hides the ${quota} placeholder in header and sidebar without removing wallet links`, async () => {
    const container = await mount(false, quota)
    assert.equal(
      container
        .querySelector('[data-testid="mobile-account-balance"]')
        ?.getAttribute('aria-label'),
      'Balance: Unavailable · Top up'
    )
    assert.match(
      container.querySelector('[data-testid="account-balance-badge"]')
        ?.textContent ?? '',
      /Unavailable/
    )
    assert.match(
      container.querySelector('[data-testid="console-balance"]')?.textContent ??
        '',
      /Unavailable/
    )
    const links = [...container.querySelectorAll('a')]
    assert.equal(links.length, 4)
    assert.ok(links.every((link) => link.getAttribute('href') === '/wallet'))
  })
}

for (const projection of [undefined, true]) {
  test(`financial projection ${projection === undefined ? 'legacy undefined' : 'true'} keeps real header and sidebar balances when only usage is unavailable`, async () => {
    const container = await mount(projection, 5_000_000)
    assert.equal(
      container
        .querySelector('[data-testid="mobile-account-balance"]')
        ?.getAttribute('aria-label'),
      'Balance: 10 USD · Top up'
    )
    assert.match(
      container.querySelector('[data-testid="account-balance-badge"]')
        ?.textContent ?? '',
      /10 USD/
    )
    assert.ok(
      container
        .querySelector('[data-testid="console-balance"]')
        ?.textContent?.includes(formatQuota(5_000_000))
    )
    assert.doesNotMatch(container.textContent ?? '', /Unavailable/)
  })
}
