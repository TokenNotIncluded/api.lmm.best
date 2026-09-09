/*
Copyright (C) 2023-2026 QuantumNous
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
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
  'HTMLInputElement',
  'HTMLButtonElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'KeyboardEvent',
  'PointerEvent',
  'MouseEvent',
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
const { getCoreRowModel, useReactTable } = await import('@tanstack/react-table')
const { createInstance } = await import('i18next')
const { initReactI18next, I18nextProvider } = await import('react-i18next')
const { CommonLogsFilterBar } = await import('../common-logs-filter-bar')
const { UsageLogsProvider, useUsageLogsContext } =
  await import('../usage-logs-provider')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: {}, fallbackLng: 'en' })

function Controls() {
  const { setSensitiveVisible, setViewScope } = useUsageLogsContext()
  return (
    <>
      <button type='button' onClick={() => setSensitiveVisible(false)}>
        Mask test
      </button>
      <button type='button' onClick={() => setSensitiveVisible(true)}>
        Reveal test
      </button>
      <button type='button' onClick={() => setViewScope('self')}>
        Personal test
      </button>
    </>
  )
}

function Fixture() {
  const table = useReactTable({
    data: [],
    columns: [],
    getCoreRowModel: getCoreRowModel(),
  })
  return (
    <UsageLogsProvider>
      <Controls />
      <CommonLogsFilterBar table={table} />
    </UsageLogsProvider>
  )
}

async function renderFilter(admin = false, failGroups = false) {
  const originalAdapter = api.defaults.adapter
  const requests: string[] = []
  useAuthStore
    .getState()
    .auth.setUser({ id: 7, username: 'tester', role: admin ? 100 : 1 })
  api.defaults.adapter = async (config) => {
    requests.push(config.url ?? '')
    const groups =
      config.url === '/api/group/' || config.url === '/api/user/self/groups'
    if (groups && failGroups) throw new Error('groups unavailable')
    return {
      config,
      headers: {},
      status: 200,
      statusText: 'OK',
      data: {
        success: true,
        data:
          config.url === '/api/group/'
            ? ['admin-only', 'default', 'auto']
            : config.url === '/api/user/self/groups'
              ? {
                  default: { ratio: 1 },
                  premium: { ratio: 2 },
                  auto: { ratio: 1 },
                }
              : { quota: 0, rpm: 0, tpm: 0 },
      },
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
      initialEntries: ['/usage-logs/common?group=historical'],
    }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    await router.load()
    root.render(
      <I18nextProvider i18n={i18n}>
        <QueryClientProvider client={client}>
          <RouterProvider router={router} />
        </QueryClientProvider>
      </I18nextProvider>
    )
  })
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 30))
  })
  return {
    container,
    router,
    requests,
    async cleanup() {
      await act(async () => root.unmount())
      client.clear()
      container.remove()
      api.defaults.adapter = originalAdapter
      useAuthStore.getState().auth.reset('idle')
    },
  }
}

function groupInput(): HTMLInputElement {
  const input = document.querySelector<HTMLInputElement>(
    'input[aria-label="Group"]'
  )
  assert.ok(input)
  return input
}
async function press(input: HTMLInputElement, key: string) {
  await act(async () => {
    input.dispatchEvent(
      new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true })
    )
  })
}
async function clickButton(label: string) {
  const button = [...document.querySelectorAll('button')].find(
    (item) => item.textContent === label
  )
  assert.ok(button, `button ${label}`)
  await act(async () => button.click())
}
function options() {
  return [...document.querySelectorAll('[role="option"]')].map((item) =>
    item.textContent?.trim()
  )
}

test('personal group choices retain history, select before Enter submits, and respect masking', async () => {
  const fixture = await renderFilter()
  try {
    const input = groupInput()
    await act(async () => input.focus())
    assert.equal(input.value, 'historical')
    assert.deepEqual(options(), ['default', 'premium'])
    assert.equal(fixture.requests.includes('/api/group/'), false)
    await act(async () => {
      const setValue = Object.getOwnPropertyDescriptor(
        HTMLInputElement.prototype,
        'value'
      )?.set
      assert.ok(setValue)
      setValue.call(input, 'def')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    assert.deepEqual(options(), ['default'])
    await press(input, 'ArrowDown')
    await press(input, 'Enter')
    assert.equal(input.value, 'default')
    assert.equal(fixture.router.state.location.search.group, 'historical')
    await press(input, 'Enter')
    assert.equal(fixture.router.state.location.search.group, 'default')
    await act(async () => {
      input.blur()
      input.focus()
    })
    await clickButton('Mask test')
    assert.equal(groupInput().type, 'password')
    assert.equal(document.querySelector('[role="listbox"]'), null)
    await clickButton('Reveal test')
    assert.equal(groupInput().value, 'default')
    await act(async () => groupInput().focus())
    await act(async () => groupInput().blur())
    assert.equal(document.querySelector('[role="listbox"]'), null)
  } finally {
    await fixture.cleanup()
  }
})

test('admin switching to personal scope loads only personal choices', async () => {
  const fixture = await renderFilter(true)
  try {
    await act(async () => groupInput().focus())
    assert.deepEqual(options(), ['admin-only', 'default'])
    await clickButton('Personal test')
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 30))
      groupInput().focus()
    })
    assert.ok(fixture.requests.includes('/api/user/self/groups'))
    assert.deepEqual(options(), ['default', 'premium'])
  } finally {
    await fixture.cleanup()
  }
})

test('failed suggestions still allow a historical custom group to be submitted', async () => {
  const fixture = await renderFilter(false, true)
  try {
    const input = groupInput()
    await act(async () => input.focus())
    assert.equal(input.value, 'historical')
    await press(input, 'Enter')
    await press(input, 'Enter')
    assert.equal(fixture.router.state.location.search.group, 'historical')
  } finally {
    await fixture.cleanup()
  }
})
