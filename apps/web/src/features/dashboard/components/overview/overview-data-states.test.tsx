/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
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
  'Element',
  'Node',
  'Event',
  'MouseEvent',
  'MutationObserver',
  'getComputedStyle',
  'requestAnimationFrame',
  'cancelAnimationFrame',
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
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { SummaryCards } = await import('./summary-cards')
const { PerformanceHealthPanel } = await import('./performance-health-panel')
const { PanelWrapper } = await import('../ui/panel-wrapper')
const originalGet = api.get
const originalUser = useAuthStore.getState().auth.user
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
let dispose: (() => Promise<void>) | undefined

afterEach(async () => {
  await dispose?.()
  dispose = undefined
  api.get = originalGet
  useAuthStore.getState().auth.setUser(originalUser)
  document.body.replaceChildren()
})
after(() => dom.close())

async function mount(Component: () => React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const route = createRootRoute({ component: Component })
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
async function until(check: () => boolean) {
  for (let i = 0; i < 100; i++) {
    if (check()) return
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10))
    })
  }
  assert.ok(check(), 'UI did not reach expected state')
}
function button(container: HTMLElement, label: string) {
  const found = [...container.querySelectorAll('button')].find(
    (el) => el.textContent === label
  )
  assert.ok(found, `Missing ${label} button`)
  return found
}

test('performance distinguishes failed responses from empty results and retries', async () => {
  let calls = 0
  api.get = (async () => ({
    data:
      ++calls === 1
        ? { success: false, data: { models: [] } }
        : { success: true, data: { models: [] } },
  })) as typeof api.get
  const container = await mount(PerformanceHealthPanel)
  await until(
    () => container.textContent?.includes('Failed to load data') === true
  )
  assert.doesNotMatch(container.textContent ?? '', /No data available/)
  await act(async () => button(container, 'Retry').click())
  await until(
    () => container.textContent?.includes('No data available') === true
  )
  assert.doesNotMatch(
    container.textContent ?? '',
    /Failed to load data|Success rate/
  )
  assert.equal(container.querySelector('.animate-pulse'), null)
  await act(async () => button(container, 'Refresh').click())
  await until(() => calls === 3)
})

test('traffic list orders by request count, not server array position', async () => {
  api.get = (async () => ({
    data: {
      success: true,
      data: {
        models: [
          {
            model_name: 'quiet-model',
            request_count: 1,
            avg_latency_ms: 100,
            avg_tps: 20,
            success_rate: 1,
          },
          {
            model_name: 'busy-model',
            request_count: 100,
            avg_latency_ms: 200,
            avg_tps: 30,
            success_rate: 1,
          },
        ],
      },
    },
  })) as typeof api.get
  const container = await mount(PerformanceHealthPanel)
  await until(() => container.textContent?.includes('busy-model') === true)
  const text = container.textContent ?? ''
  assert.ok(text.indexOf('busy-model') < text.indexOf('quiet-model'))
})

test('failed usage stays unknown and a new account fetches its own usage', async () => {
  type User = NonNullable<typeof originalUser>
  const user = {
    id: 901,
    username: 'qa',
    role: 1,
    quota: 5000000,
    used_quota: 1000,
    request_count: 3,
    group: 'default',
  } as User
  useAuthStore.getState().auth.setUser(user)
  let usageCalls = 0
  api.get = (async (url: string) => {
    if (url === '/api/status') {
      return {
        data: {
          success: true,
          data: { system_name: 'Test', display_in_currency: true },
        },
      }
    }
    if (url === '/api/data/self') {
      usageCalls++
      return { data: { success: usageCalls > 1, data: [] } }
    }
    throw new Error(`Unexpected ${url}`)
  }) as typeof api.get
  const container = await mount(SummaryCards)
  await until(
    () => container.textContent?.includes('Failed to load data') === true
  )
  assert.doesNotMatch(container.textContent ?? '', /Healthy|No recent usage/)
  assert.match(container.textContent ?? '', /Unknown/)
  await act(async () =>
    useAuthStore.getState().auth.setUser({ ...user, id: 902 })
  )
  await until(
    () =>
      usageCalls === 2 &&
      container.textContent?.includes('No recent usage') === true
  )
  assert.doesNotMatch(container.textContent ?? '', /Failed to load data/)
})

test('empty and loading panels preserve their header action', async () => {
  let clicks = 0
  const container = await mount(() => (
    <>
      <PanelWrapper
        title='Empty'
        empty
        headerActions={
          <button type='button' onClick={() => clicks++}>
            Refresh empty
          </button>
        }
      />
      <PanelWrapper
        title='Loading'
        loading
        headerActions={
          <button type='button' onClick={() => clicks++}>
            Refresh loading
          </button>
        }
      />
    </>
  ))
  await act(async () => {
    button(container, 'Refresh empty').click()
    button(container, 'Refresh loading').click()
  })
  assert.equal(clicks, 2)
})
