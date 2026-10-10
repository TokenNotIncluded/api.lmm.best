/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window({ url: 'http://localhost' })
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
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
after(() => dom.close())

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} = await import('@tanstack/react-router')
const { createInstance } = await import('i18next')
const { initReactI18next, I18nextProvider } = await import('react-i18next')
const { CommonLogsStats } = await import('../common-logs-stats')
const { UsageLogsProvider, useUsageLogsContext } =
  await import('../usage-logs-provider')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')

const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: {}, fallbackLng: 'en' })

function Fixture() {
  const { setSensitiveVisible } = useUsageLogsContext()
  return (
    <>
      <button type='button' onClick={() => setSensitiveVisible(false)}>
        Hide amount
      </button>
      <button type='button' onClick={() => setSensitiveVisible(true)}>
        Show amount
      </button>
      <CommonLogsStats />
    </>
  )
}

for (const admin of [false, true]) {
  test(`masking ${admin ? 'admin' : 'personal'} usage removes historical trend requests but keeps live RPM`, async () => {
    const originalAdapter = api.defaults.adapter
    const requests: string[] = []
    useAuthStore
      .getState()
      .auth.setUser({ id: 7, username: 'tester', role: admin ? 100 : 1 })
    api.defaults.adapter = async (config) => {
      requests.push(config.url ?? '')
      return {
        config,
        headers: {},
        status: 200,
        statusText: 'OK',
        data: { success: true, data: { quota: 100, rpm: 3, tpm: 9 } },
      }
    }
    const rootRoute = createRootRoute()
    const authRoute = createRoute({
      getParentRoute: () => rootRoute,
      id: '_authenticated',
    })
    const logsRoute = createRoute({
      getParentRoute: () => authRoute,
      path: '/usage-logs/$section',
      component: Fixture,
      validateSearch: (search: Record<string, unknown>) => search,
    })
    const router = createRouter({
      routeTree: rootRoute.addChildren([authRoute.addChildren([logsRoute])]),
      history: createMemoryHistory({
        initialEntries: [
          '/usage-logs/common?startTime=1704067200000&endTime=1704153600000',
        ],
      }),
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    try {
      await act(async () => {
        await router.load()
        root.render(
          <I18nextProvider i18n={i18n}>
            <QueryClientProvider client={client}>
              <UsageLogsProvider>
                <RouterProvider router={router} />
              </UsageLogsProvider>
            </QueryClientProvider>
          </I18nextProvider>
        )
      })
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 30))
      })
      assert.ok(
        container.querySelector(
          '[aria-label="Usage trend for the selected time range"]'
        )
      )
      const endpoint = admin ? '/api/log/stat' : '/api/log/self/stat'
      assert.equal(requests.length, 13)
      assert.ok(requests.every((request) => request.startsWith(endpoint)))
      const before = requests.length
      const hide = [...container.querySelectorAll('button')].find(
        (button) => button.textContent === 'Hide amount'
      )
      assert.ok(hide)
      await act(async () => hide.click())
      assert.equal(
        container.querySelector(
          '[aria-label="Usage trend for the selected time range"]'
        ),
        null
      )
      assert.match(container.textContent ?? '', /RPM\s*3/)
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 30))
      })
      assert.equal(requests.length, before)
      // Changing the historical range while masked must refresh only the badges.
      await act(async () => {
        router.history.push(
          '/usage-logs/common?startTime=1704153600000&endTime=1704240000000'
        )
      })
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 30))
      })
      assert.equal(requests.length, before + 1)
      assert.match(container.textContent ?? '', /RPM\s*3/)
      assert.match(container.textContent ?? '', /TPM\s*9/)
      assert.ok(container.textContent?.includes('••••'))
      const show = [...container.querySelectorAll('button')].find(
        (button) => button.textContent === 'Show amount'
      )
      assert.ok(show)
      await act(async () => show.click())
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 30))
      })
      assert.equal(requests.length, before + 13)
      const trend = container.querySelector(
        '[aria-label="Usage trend for the selected time range"]'
      )
      assert.ok(trend)
      const bars = trend.querySelectorAll('button')
      assert.equal(bars.length, 12)
      assert.ok([...bars].every((bar) => !bar.title.includes('RPM')))
      const first = bars[0]
      assert.ok(first)
      await act(async () => first.click())
      assert.equal(
        Number(router.state.location.search.startTime),
        1704153600000
      )
      assert.equal(Number(router.state.location.search.endTime), 1704160800000)
      assert.equal(router.state.location.search.page, 1)
    } finally {
      await act(async () => root.unmount())
      client.clear()
      container.remove()
      api.defaults.adapter = originalAdapter
      useAuthStore.getState().auth.reset('idle')
    }
  })
}
