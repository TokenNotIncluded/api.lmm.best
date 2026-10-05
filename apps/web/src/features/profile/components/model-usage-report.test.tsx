/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window({ url: 'https://console.example.test/profile/share' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'getComputedStyle',
  'scrollTo',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { RouterProvider, createMemoryHistory, createRootRoute, createRouter } =
  await import('@tanstack/react-router')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { ThemeProvider } = await import('@/context/theme-provider')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { DEFAULT_CURRENCY_CONFIG, useSystemConfigStore } =
  await import('@/stores/system-config-store')
const { ModelUsageReport } = await import('./model-usage-report')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
after(() => dom.close())

test('model usage and copied report update units and FX without refetching or changing Credits', async () => {
  const previousAuth = useAuthStore.getState().auth
  const previousCurrency = useSystemConfigStore.getState().config.currency
  const previousGet = api.get
  let calls = 0
  let markdown = ''
  const updateCopy = (snapshot: { markdown: string } | null) => {
    markdown = snapshot?.markdown ?? ''
  }
  const user = {
    id: 7,
    username: 'alice',
    role: 1,
    setting: { wallet_display_currency: 'USD' },
  }
  useAuthStore.getState().auth.setUser(user)
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...DEFAULT_CURRENCY_CONFIG,
      currencyUnit: 'credit',
      creditsPerUsd: 3_500_000,
      creditsPerUsdExact: '3500000',
      cnyPerUsd: 7,
      cnyPerUsdExact: '7',
    },
  })
  api.get = (async () => {
    calls += 1
    return {
      data: {
        success: true,
        data: [
          {
            created_at: 1_720_000_000,
            model_name: 'model-a',
            quota: 3_500_001,
            token_used: 100,
            count: 1,
          },
        ],
      },
    }
  }) as typeof api.get
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  const route = createRootRoute({
    component: () => (
      <QueryClientProvider client={client}>
        <I18nextProvider i18n={i18n}>
          <ThemeProvider defaultTheme='light'>
            <ModelUsageReport rangeKey='7d' onCopySnapshotChange={updateCopy} />
          </ThemeProvider>
        </I18nextProvider>
      </QueryClientProvider>
    ),
  })
  const router = createRouter({
    routeTree: route,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  try {
    await act(async () => {
      root.render(<RouterProvider router={router} />)
    })
    for (let attempt = 0; !markdown && attempt < 30; attempt += 1) {
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 5))
      })
    }
    assert.ok(markdown, 'the copied report did not become available')
    assert.match(markdown, /1 USD/)
    await act(async () =>
      useAuthStore
        .getState()
        .auth.setUser({ ...user, setting: { wallet_display_currency: 'CNY' } })
    )
    assert.match(markdown, /7 CNY/)
    assert.match(host.textContent ?? '', /7 CNY/)
    await act(async () =>
      useSystemConfigStore.getState().setConfig({
        currency: {
          ...useSystemConfigStore.getState().config.currency,
          cnyPerUsd: 8,
          cnyPerUsdExact: '8',
        },
      })
    )
    assert.match(markdown, /8 CNY/)
    await act(async () =>
      useAuthStore.getState().auth.setUser({
        ...user,
        setting: { wallet_display_currency: 'CREDIT' },
      })
    )
    assert.match(markdown, /3,500,001 Credits/)
    await act(async () => i18n.changeLanguage('zhCN'))
    assert.match(markdown, /3,500,001 Credits/)
    assert.equal(calls, 1)
    assert.doesNotMatch(markdown, /Platform|\$/)
  } finally {
    await act(async () => root.unmount())
    client.clear()
    host.remove()
    api.get = previousGet
    useAuthStore.setState({ auth: previousAuth })
    useSystemConfigStore.getState().setConfig({ currency: previousCurrency })
  }
})
