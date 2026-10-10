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
const { AdminSiteStatisticsPanel } = await import('./admin-site-statistics')
const { DEFAULT_CURRENCY_CONFIG, useSystemConfigStore } =
  await import('@/stores/system-config-store')
const { PerformanceHealthPanel } = await import('./performance-health-panel')
const { PanelWrapper } = await import('../ui/panel-wrapper')
const originalGet = api.get
const originalPut = api.put
const originalConfig = useSystemConfigStore.getState().config
const originalUser = useAuthStore.getState().auth.user
const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: { credits: 'Credits' } } },
})
let dispose: (() => Promise<void>) | undefined

afterEach(async () => {
  await dispose?.()
  dispose = undefined
  api.get = originalGet
  api.put = originalPut
  useSystemConfigStore.setState({ config: originalConfig })
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
  assert.match(container.textContent ?? '', /1,000 Credits/)
  assert.match(container.textContent ?? '', /Total consumed \(Credits\)/)
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

test('successful null usage renders an empty overview without crashing', async () => {
  type User = NonNullable<typeof originalUser>
  useAuthStore.getState().auth.setUser({
    id: 903,
    username: 'empty-usage',
    role: 1,
    quota: 5000000,
    used_quota: 0,
    request_count: 0,
    group: 'default',
  } as User)
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
      return { data: { success: true, data: null } }
    }
    throw new Error(`Unexpected ${url}`)
  }) as typeof api.get
  const container = await mount(SummaryCards)
  await until(() => container.textContent?.includes('No recent usage') === true)
  assert.match(container.textContent ?? '', /Usage at a glance/)
  assert.match(container.textContent ?? '', /Last 24h usage/)
  assert.doesNotMatch(
    container.textContent ?? '',
    /Failed to load data|Unknown/
  )
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

function configureCurrencyOverview() {
  const currency = {
    ...DEFAULT_CURRENCY_CONFIG,
    currencyUnit: 'credit' as const,
    creditsPerUsd: 500000,
    creditsPerUsdExact: '500000',
    cnyPerUsd: 7,
    cnyPerUsdExact: '7',
  }
  useSystemConfigStore.getState().setConfig({ currency })
  useSystemConfigStore.getState().setLoading(false)
  useAuthStore.getState().auth.setUser({
    id: 905,
    username: 'review',
    role: 10,
    quota: 5000000,
    used_quota: 1000000,
    normalized_used_quota: 1000000,
    usage_projection_available: true,
    request_count: 20,
    setting: {
      wallet_display_currency: 'USD',
      settlement_currency: 'CNY',
      language: 'en',
    },
  })
  api.get = (async (url: string) => {
    if (url === '/api/finance/site-statistics') {
      return {
        data: {
          success: true,
          data: {
            as_of: 1791504000,
            credits_per_usd: 500000,
            total_used_credits: '9277415232383220730',
            total_balance_credits: '500000000',
            recharge: {
              currencies: [
                {
                  currency: 'USD',
                  gross_amount_micros: '3000000',
                  refunded_amount_micros: '250000',
                  net_amount_micros: '2750000',
                  orders: 1,
                },
              ],
              virtual_units: [],
              confirmed_orders: 1,
              unconfirmed_orders: 0,
              invalid_orders: 0,
            },
          },
        },
      }
    }
    if (url === '/api/status') {
      return {
        data: {
          success: true,
          data: {
            system_name: 'Review',
            currency_unit: 'credit',
            credits_per_usd: 500000,
            cny_per_usd:
              useSystemConfigStore.getState().config.currency.cnyPerUsd,
          },
        },
      }
    }
    if (url === '/api/data/self') {
      return { data: { success: true, data: [{ quota: 250000 }] } }
    }
    throw new Error(`Unexpected ${url}`)
  }) as typeof api.get
}

function currencyButton(container: HTMLElement, currency: string) {
  const element = container.querySelector<HTMLButtonElement>(
    `[data-currency="${currency}"]`
  )
  assert.ok(element)
  return element
}

function CurrencyOverview() {
  return (
    <>
      <SummaryCards />
      <AdminSiteStatisticsPanel />
    </>
  )
}

test('quick switch saves only the preference and updates all quota displays after acknowledgement', async () => {
  configureCurrencyOverview()
  const writes: unknown[] = []
  let acknowledge: ((value: { data: { success: boolean } }) => void) | undefined
  api.put = (async (url, body) => {
    assert.equal(url, '/api/user/self')
    writes.push(body)
    if (writes.length === 1) {
      return new Promise<{ data: { success: boolean } }>((resolve) => {
        acknowledge = resolve
      })
    }
    return { data: { success: true } }
  }) as typeof api.put
  let container = await mount(CurrencyOverview)
  await until(
    () =>
      container
        .querySelector('.overview-balance-value')
        ?.textContent?.includes('10 USD') === true
  )
  await until(
    () => container.querySelectorAll('.overview-payment-row').length === 1
  )
  const payments = container.querySelector('.overview-payments')?.textContent
  await act(async () => currencyButton(container, 'CNY').click())
  assert.equal(
    container
      .querySelector('.overview-currency-switch')
      ?.getAttribute('aria-busy'),
    'true'
  )
  assert.ok(
    [...container.querySelectorAll<HTMLButtonElement>('[data-currency]')].every(
      (button) => button.disabled
    )
  )
  assert.match(
    container.querySelector('.overview-balance-value')?.textContent ?? '',
    /10 USD/
  )
  await act(async () => {
    assert.ok(acknowledge)
    acknowledge({ data: { success: true } })
  })
  await until(
    () =>
      container
        .querySelector('.overview-balance-value')
        ?.textContent?.includes('70 CNY') === true
  )
  assert.match(
    container.querySelector('.overview-metrics')?.textContent ?? '',
    /3.5 CNY/
  )
  assert.match(
    container.querySelector('.overview-metrics')?.textContent ?? '',
    /14 CNY/
  )
  assert.match(
    container.querySelector('.overview-site-totals')?.textContent ?? '',
    /7,000 CNY/
  )
  assert.equal(
    container.querySelector('.overview-payments')?.textContent,
    payments
  )
  await act(async () => currencyButton(container, 'CREDIT').click())
  await until(
    () =>
      currencyButton(container, 'CREDIT').getAttribute('aria-pressed') ===
      'true'
  )
  assert.match(
    container.querySelector('.overview-balance-value')?.textContent ?? '',
    /5,000,000 Credits/
  )
  assert.match(
    container.querySelector('.overview-site-value')?.textContent ?? '',
    /9,277,415,232,383,220,730 Credits/
  )
  assert.equal(
    container.querySelector('.overview-payments')?.textContent,
    payments
  )
  await act(async () => currencyButton(container, 'CREDIT').click())
  assert.equal(
    currencyButton(container, 'CREDIT').getAttribute('aria-pressed'),
    'true'
  )
  assert.deepEqual(writes, [
    { wallet_display_currency: 'CNY' },
    { wallet_display_currency: 'CREDIT' },
  ])
  assert.deepEqual(
    JSON.parse(String(useAuthStore.getState().auth.user?.setting)),
    {
      wallet_display_currency: 'CREDIT',
      settlement_currency: 'CNY',
      language: 'en',
    }
  )
  await dispose?.()
  dispose = undefined
  container = await mount(CurrencyOverview)
  assert.equal(
    currencyButton(container, 'CREDIT').getAttribute('aria-pressed'),
    'true'
  )
  assert.equal(writes.length, 2)
})

test('quick-switch save failures retain the current unit and can be retried', async () => {
  configureCurrencyOverview()
  let calls = 0
  api.put = (async () => ({ data: { success: ++calls > 1 } })) as typeof api.put
  const container = await mount(CurrencyOverview)
  await act(async () => currencyButton(container, 'CNY').click())
  await until(() => container.querySelector('[role="alert"]') !== null)
  assert.match(
    container.querySelector('[role="alert"]')?.textContent ?? '',
    /Could not save balance display currency/
  )
  assert.equal(
    currencyButton(container, 'USD').getAttribute('aria-pressed'),
    'true'
  )
  await act(async () => currencyButton(container, 'CNY').click())
  await until(
    () =>
      currencyButton(container, 'CNY').getAttribute('aria-pressed') === 'true'
  )
  assert.equal(container.querySelector('[role="alert"]'), null)
  assert.equal(calls, 2)
})

test('missing FX cannot turn site balances into zero or alter cash payments', async () => {
  configureCurrencyOverview()
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...useSystemConfigStore.getState().config.currency,
      cnyPerUsd: 0,
    },
  })
  api.put = (async () => ({ data: { success: true } })) as typeof api.put
  const container = await mount(CurrencyOverview)
  await until(
    () => container.querySelectorAll('.overview-site-value').length === 2
  )
  await act(async () => currencyButton(container, 'CNY').click())
  await until(
    () =>
      currencyButton(container, 'CNY').getAttribute('aria-pressed') === 'true'
  )
  assert.equal(
    container.querySelector('.overview-balance-value')?.textContent,
    '- CNY'
  )
  assert.ok(
    [...container.querySelectorAll('.overview-site-value')].every(
      (value) => value.textContent === '- CNY'
    )
  )
  assert.match(
    container.querySelector('.overview-payments')?.textContent ?? '',
    /2.75 USD/
  )
  assert.match(
    container.querySelector('.overview-exact-credits')?.textContent ?? '',
    /9,277,415,232,383,220,730/
  )
})
