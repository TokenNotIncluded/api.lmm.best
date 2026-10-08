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
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { INTERFACE_LANGUAGE_OPTIONS } = await import('@/i18n/languages')
const { useAuthStore } = await import('@/stores/auth-store')
const { AdminSiteStatisticsPanel } = await import('./admin-site-statistics')
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
  await i18n.changeLanguage('en')
  document.body.replaceChildren()
})
after(() => dom.close())

function user(role: number, id = 902) {
  return {
    id,
    role,
    username: 'private-user',
    quota: 5_000_000,
    used_quota: 250_000,
    request_count: 1,
    group: 'default',
  } as NonNullable<typeof originalUser>
}
const data = {
  as_of: 1791331200,
  credits_per_usd: 500000,
  total_used_credits: '9277415232383220730',
  total_balance_credits: '4750000',
  recharge: {
    currencies: [
      {
        currency: 'USD',
        gross_amount_micros: '1000000',
        refunded_amount_micros: '250001',
        net_amount_micros: '749999',
        orders: 1,
      },
      {
        currency: 'CNY',
        gross_amount_micros: '7333333',
        refunded_amount_micros: '0',
        net_amount_micros: '7333333',
        orders: 1,
      },
    ],
    virtual_units: [
      {
        currency: 'LDC',
        gross_amount_micros: '5000000',
        refunded_amount_micros: '1000001',
        net_amount_micros: '3999999',
        orders: 1,
      },
    ],
    confirmed_orders: 3,
    unconfirmed_orders: 1,
    invalid_orders: 1,
  },
}
async function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () =>
    root.render(
      <QueryClientProvider client={client}>
        <I18nextProvider i18n={i18n}>
          <AdminSiteStatisticsPanel />
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
  for (let i = 0; i < 100 && !check(); i++) {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10))
    })
  }
  assert.ok(check(), 'Expected site statistics UI state')
}

test('ordinary accounts never fetch or render administrator site totals', async () => {
  useAuthStore.getState().auth.setUser(user(1))
  let calls = 0
  api.get = (async () => {
    calls++
    return { data: { success: true, data } }
  }) as typeof api.get
  const container = await mount()
  assert.equal(calls, 0)
  assert.equal(container.textContent, '')
})

test('admins see exact credits and original-currency discounted payment and refunds', async () => {
  useAuthStore.getState().auth.setUser(user(10))
  let requested = ''
  api.get = (async (url: string) => {
    requested = url
    return { data: { success: true, data } }
  }) as typeof api.get
  const container = await mount()
  await until(() => container.textContent?.includes('1.00 USD') === true)
  const text = container.textContent ?? ''
  assert.equal(requested, '/api/finance/site-statistics')
  assert.match(text, /9,277,415,232,383,220,730/)
  assert.match(text, /4,750,000/)
  assert.match(text, /0\.250001 USD/)
  assert.match(text, /0\.749999 USD/)
  assert.match(text, /7\.333333 CNY/)
  assert.match(text, /充值优惠可能增加到账点数/)
  assert.match(text, /非现金点数单独列示/)
  const cash = container.querySelector(
    '[aria-labelledby="site-statistics-cash-title"]'
  )
  const points = container.querySelector(
    '[aria-labelledby="site-statistics-virtual-title"]'
  )
  assert.ok(cash && points)
  assert.doesNotMatch(cash.textContent ?? '', /LDC/)
  assert.match(points.textContent ?? '', /5\.00 LDC/)
  assert.match(points.textContent ?? '', /3\.999999 LDC/)
  assert.doesNotMatch(points.textContent ?? '', /USD|CNY/)
  assert.match(text, /缺少可信实付证据：1 笔/)
  assert.doesNotMatch(text, /private-user|5,000,000/)
  await act(async () => useAuthStore.getState().auth.setUser(user(1)))
  assert.equal(
    container.textContent,
    '',
    'demotion removes already-loaded aggregate data'
  )
})

for (const { code } of INTERFACE_LANGUAGE_OPTIONS) {
  test(`administrator statistics render with the ${code} interface language`, async () => {
    useAuthStore.getState().auth.setUser(user(10))
    await i18n.changeLanguage(code)
    api.get = (async () => ({
      data: { success: true, data },
    })) as typeof api.get
    const container = await mount()
    const locale = code === 'zhCN' ? 'zh-CN' : code === 'zhTW' ? 'zh-TW' : code
    const total = new Intl.NumberFormat(locale).format(
      BigInt(data.total_used_credits)
    )
    await until(() => container.textContent?.includes(total) === true)
    assert.doesNotMatch(container.textContent ?? '', /当前金额未确认/)
    assert.match(container.textContent ?? '', /USD|CNY|LDC/)
  })
}

test('failed or malformed sources stay unknown and explicit refresh recovers', async () => {
  useAuthStore.getState().auth.setUser(user(100))
  let calls = 0
  api.get = (async () => ({
    data:
      ++calls === 1
        ? { success: true, data: { ...data, total_balance_credits: 4750000 } }
        : { success: true, data },
  })) as typeof api.get
  const container = await mount()
  await until(() => container.textContent?.includes('当前金额未确认') === true)
  assert.doesNotMatch(
    container.textContent ?? '',
    /0\.00 USD|4,750,000|暂无已确认/
  )
  const refresh = [...container.querySelectorAll('button')].find(
    (button) => button.textContent === 'Refresh'
  )
  assert.ok(refresh)
  await act(async () => refresh.click())
  await until(() => container.textContent?.includes('1.00 USD') === true)
  assert.equal(calls, 2)
})
