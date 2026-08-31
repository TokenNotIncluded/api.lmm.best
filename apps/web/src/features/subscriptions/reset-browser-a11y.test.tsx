/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

const domWindow = new Window({
  url: 'https://console.example.test/subscriptions/reset',
})
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
  'HTMLInputElement',
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
  'localStorage',
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
const {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} = await import('@tanstack/react-router')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { SidebarProvider } = await import('@/components/ui/sidebar')
const { api } = await import('@/lib/api')
const { SubscriptionResetWorkspace } = await import('./reset-workspace')
const { SubscriptionResetVouchers } =
  await import('../wallet/components/subscription-reset-vouchers')

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

const originalGet = api.get

type Deferred<T> = {
  promise: Promise<T>
  resolve: (value: T) => void
  reject: (reason?: unknown) => void
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((nextResolve, nextReject) => {
    resolve = nextResolve
    reject = nextReject
  })
  return { promise, resolve, reject }
}

async function flushQueries() {
  await new Promise((resolve) => setTimeout(resolve, 25))
}

async function waitFor(
  condition: () => boolean,
  failureMessage: string
): Promise<void> {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    if (condition()) return
    await act(flushQueries)
  }
  throw new Error(`${failureMessage}: ${document.body.textContent}`)
}

function buttonNamed(container: HTMLElement, name: string) {
  return [...container.querySelectorAll<HTMLButtonElement>('button')].find(
    (button) => button.textContent?.trim() === name
  )
}

async function renderWithQuery(
  node: React.ReactNode,
  queryClient: InstanceType<typeof QueryClient>
) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>{node}</I18nextProvider>
      </QueryClientProvider>
    )
  })
  return { container, queryClient, root }
}

async function renderWorkspace(queryClient: InstanceType<typeof QueryClient>) {
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <SidebarProvider>
            <SubscriptionResetWorkspace />
          </SidebarProvider>
        </I18nextProvider>
      </QueryClientProvider>
    ),
  })
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/',
    component: () => null,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(<RouterProvider router={router} />)
    await flushQueries()
  })
  return { container, queryClient, root }
}

async function unmount(rendered: {
  root: ReturnType<typeof createRoot>
  queryClient: InstanceType<typeof QueryClient>
}) {
  await act(async () => rendered.root.unmount())
  rendered.queryClient.clear()
}

afterEach(() => {
  api.get = originalGet
  document.body.replaceChildren()
})

after(() => domWindow.close())

const eligibleTarget = {
  user_id: 7,
  username: 'alice',
  email: 'alice@example.test',
  plan_id: 3,
  plan_title: 'Pro',
  plan_archived_at: 0,
  active_subscription_count: 2,
  amount_total: 20_000,
  amount_used: 5_000,
  next_reset_time: 1_900_000_000,
  banked_voucher_count: 0,
}

const eligibleResponse = {
  success: true,
  data: {
    items: [eligibleTarget],
    total: 1,
    page: 1,
    page_size: 20,
  },
}

const voucher = {
  id: 11,
  user_id: 7,
  plan_id: 3,
  plan_title: 'Pro',
  operation_id: 'reset-op',
  status: 'available' as const,
  expires_at: 1_900_000_000,
  redeemed_at: 0,
  created_at: 1_800_000_000,
}

describe('subscription reset browser accessibility', () => {
  test('retains rows while refetching, announces progress, and locks destructive selection', async () => {
    const refetch = deferred<{ data: typeof eligibleResponse }>()
    api.get = (async (url) => {
      if (url === '/api/subscription/root/reset-targets') {
        return refetch.promise
      }
      return { data: { success: true, data: [] } }
    }) as typeof api.get

    const queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false, staleTime: Number.POSITIVE_INFINITY },
      },
    })
    queryClient.setQueryData(['admin-subscription-plans', 'reset-workspace'], {
      success: true,
      data: [],
    })
    queryClient.setQueryData(
      ['subscription-reset-eligible', 1, '', '', ''],
      eligibleResponse
    )

    const rendered = await renderWorkspace(queryClient)
    await waitFor(
      () =>
        [...rendered.container.querySelectorAll('tr')].some((row) =>
          row.textContent?.includes('alice')
        ),
      'eligible row did not render'
    )
    const eligibleRow = [...rendered.container.querySelectorAll('tr')].find(
      (row) => row.textContent?.includes('alice')
    )
    const rowCheckbox = eligibleRow?.querySelector<HTMLElement>(
      '[data-slot="checkbox"]'
    )
    assert.ok(rowCheckbox)
    assert.equal(rowCheckbox.getAttribute('aria-label'), 'Select alice on Pro')
    assert.equal(rowCheckbox.getAttribute('aria-disabled'), null)
    await act(async () => rowCheckbox.click())

    const preparePreview = buttonNamed(rendered.container, 'Prepare preview')
    assert.ok(preparePreview)
    assert.equal(preparePreview.disabled, false)

    await act(async () => {
      void queryClient.invalidateQueries({
        queryKey: ['subscription-reset-eligible'],
      })
      await flushQueries()
    })

    const busyRegion = rendered.container.querySelector(
      '[aria-busy][aria-describedby="subscription-reset-query-status"]'
    )
    assert.ok(busyRegion)
    assert.equal(busyRegion.getAttribute('aria-busy'), 'true')
    const status = rendered.container.querySelector(
      '#subscription-reset-query-status[role="status"][aria-live="polite"]'
    )
    assert.equal(status?.closest('[aria-busy="true"]'), null)
    assert.equal(status?.textContent?.trim(), 'Refreshing...')
    assert.match(rendered.container.textContent ?? '', /alice/)
    assert.equal(rowCheckbox.getAttribute('aria-disabled'), 'true')
    assert.equal(preparePreview.disabled, true)

    refetch.resolve({ data: eligibleResponse })
    await waitFor(
      () => busyRegion.getAttribute('aria-busy') === 'false',
      'eligible query did not settle'
    )
    assert.equal(status?.textContent?.trim(), '')

    await unmount(rendered)
  })

  test('announces voucher loading, retained-data errors, and focus-safe retries', async () => {
    const initial = deferred<{
      data: { success: boolean; data: (typeof voucher)[] }
    }>()
    const refresh = deferred<{
      data: { success: boolean; data: (typeof voucher)[] }
    }>()
    const retry = deferred<{
      data: { success: boolean; data: (typeof voucher)[] }
    }>()
    let requests = 0
    api.get = (async (url) => {
      if (url !== '/api/subscription/self/reset-vouchers') {
        return { data: { success: true, data: [] } }
      }
      requests += 1
      if (requests === 1) return initial.promise
      if (requests === 2) return refresh.promise
      return retry.promise
    }) as typeof api.get

    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const rendered = await renderWithQuery(
      <SubscriptionResetVouchers />,
      queryClient
    )
    const busyRegion = rendered.container.querySelector(
      '[aria-busy][aria-describedby="banked-reset-query-status"]'
    )
    const status = rendered.container.querySelector(
      '#banked-reset-query-status[role="status"][aria-live="polite"]'
    )
    assert.ok(busyRegion)
    assert.equal(busyRegion.getAttribute('aria-busy'), 'true')
    assert.equal(status?.closest('[aria-busy="true"]'), null)
    assert.equal(status?.textContent?.trim(), 'Loading...')

    initial.resolve({ data: { success: true, data: [voucher] } })
    await waitFor(
      () => rendered.container.textContent?.includes('Pro') === true,
      'voucher did not load'
    )

    const refreshButton = buttonNamed(rendered.container, 'Refresh')
    assert.ok(refreshButton)
    refreshButton.focus()
    await act(async () => {
      refreshButton.click()
      await flushQueries()
    })
    assert.equal(busyRegion.getAttribute('aria-busy'), 'true')
    assert.equal(status?.textContent?.trim(), 'Refreshing...')
    assert.match(rendered.container.textContent ?? '', /Pro/)
    assert.equal(document.activeElement, refreshButton)

    refresh.reject(new Error('offline'))
    await waitFor(
      () => buttonNamed(rendered.container, 'Retry') != null,
      'voucher error did not render'
    )
    const alert = rendered.container.querySelector(
      '#banked-reset-query-error[role="alert"][aria-live="assertive"]'
    )
    assert.equal(alert?.textContent?.trim(), 'Failed to load banked resets')
    assert.match(rendered.container.textContent ?? '', /Pro/)
    const retryButton = buttonNamed(rendered.container, 'Retry')
    assert.equal(retryButton, refreshButton)
    assert.equal(document.activeElement, refreshButton)

    await act(async () => {
      retryButton?.click()
      await flushQueries()
    })
    assert.equal(status?.textContent?.trim(), 'Refreshing...')
    assert.equal(document.activeElement, refreshButton)

    retry.resolve({ data: { success: true, data: [voucher] } })
    await waitFor(
      () => buttonNamed(rendered.container, 'Refresh') != null,
      'voucher retry did not settle'
    )
    assert.equal(
      rendered.container.querySelector('#banked-reset-query-error'),
      null
    )
    assert.equal(document.activeElement, refreshButton)

    await unmount(rendered)
  })
})
