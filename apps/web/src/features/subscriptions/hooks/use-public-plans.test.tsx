/* Copyright (C) 2026 LIghtJUNction */
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

import type { ApiRequestConfig } from '@/lib/api'

const dom = new Window({ url: 'https://console.example.test/wall' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Node',
  'Element',
  'Event',
  'MutationObserver',
  'localStorage',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: key === 'window' ? dom : dom[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
Object.defineProperty(dom.navigator, 'language', {
  configurable: true,
  value: 'en-US',
})
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { getWaffoPancakeCheckoutLanguage } =
  await import('@/lib/waffo-pancake-checkout')
const { expectedSettlement } =
  await import('@/features/wallet/lib/settlement-quote')
const { usePublicPlans } = await import('./use-public-plans')
const originalGet = api.get
after(() => {
  api.get = originalGet
  dom.happyDOM.abort()
})

type Observation = {
  language: string | undefined
  signal: ApiRequestConfig['signal']
}
// This mock models only locale transport: it is not a Go/provider or FX oracle.
function observeQuotes(explicitUsd = false, pending = false) {
  const calls: Observation[] = []
  api.get = (async (url, config) => {
    assert.equal(url, '/api/subscription/plans')
    const language =
      new Headers(config?.headers as HeadersInit).get('Accept-Language') ??
      undefined
    calls.push({ language, signal: config?.signal })
    if (pending) return await new Promise(() => {})
    const currency = explicitUsd || !language?.startsWith('zh') ? 'USD' : 'CNY'
    return {
      data: {
        success: true,
        data: [
          {
            plan: { id: 159, price_amount: 159, currency: 'CNY' },
            waffo_pancake_options: [
              {
                product_type: 'one_time',
                auto_renew: false,
                settlement: {
                  available: true,
                  amount: currency === 'USD' ? '23.73' : '159.00',
                  currency,
                },
              },
            ],
          },
        ],
      },
    }
  }) as typeof api.get
  return calls
}

async function mount(language: string) {
  const i18n = createInstance()
  await i18n.use(initReactI18next).init({
    lng: language,
    fallbackLng: false,
    resources: {
      en: { translation: {} },
      zhCN: { translation: {} },
      zhTW: { translation: {} },
    },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  let value: ReturnType<typeof usePublicPlans> | undefined
  function Harness() {
    value = usePublicPlans()
    return null
  }
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () =>
    root.render(
      <I18nextProvider i18n={i18n}>
        <QueryClientProvider client={client}>
          <Harness />
        </QueryClientProvider>
      </I18nextProvider>
    )
  )
  return {
    i18n,
    client,
    current: () => {
      assert.ok(value)
      return value
    },
    close: async () => {
      await act(async () => root.unmount())
      client.clear()
      container.remove()
    },
  }
}

async function until(condition: () => boolean) {
  for (let i = 0; i < 100; i++) {
    if (condition()) return
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 5))
    })
  }
  assert.ok(condition(), 'hook did not reach the expected state')
}

function settlement(result: ReturnType<typeof usePublicPlans>) {
  const quote = result.data?.[0]?.waffo_pancake_options?.[0]?.settlement
  assert.ok(quote, 'server must supply a per-mode settlement quote')
  return quote
}

// @mark payments.settlement-locale role=test
test('browser English and Chinese UI quote with the same language as checkout', async () => {
  const calls = observeQuotes()
  const view = await mount('zhCN')
  try {
    await until(() => view.current().isSuccess)
    assert.equal(navigator.language, 'en-US')
    const checkoutLanguage = getWaffoPancakeCheckoutLanguage(
      view.i18n.resolvedLanguage || view.i18n.language
    )
    const quote = settlement(view.current())
    const pair = expectedSettlement(quote)
    assert.deepEqual(
      { requestLanguage: calls[0].language, checkoutLanguage, ...pair },
      {
        requestLanguage: 'zh-Hans',
        checkoutLanguage: 'zh-Hans',
        settlement_amount: '159.00',
        settlement_currency: 'CNY',
      },
      'without the header the mock returns the stale USD 23.73 pair while checkout uses Chinese'
    )
    assert.ok(calls[0].signal instanceof AbortSignal)
  } finally {
    await view.close()
  }
})

test('changing UI locale fetches a new server quote and does not reuse English data', async () => {
  const calls = observeQuotes()
  const view = await mount('en')
  try {
    await until(() => view.current().isSuccess)
    assert.equal(settlement(view.current()).currency, 'USD')
    await act(async () => {
      await view.i18n.changeLanguage('zhTW')
    })
    await until(() => calls.length >= 2 && view.current().isSuccess)
    assert.deepEqual(
      calls.map((call) => call.language),
      ['en', 'zh-Hant-TW']
    )
    assert.equal(settlement(view.current()).amount, '159.00')
  } finally {
    await view.close()
  }
})

test('explicit server USD preference is preserved despite Chinese UI', async () => {
  observeQuotes(true)
  const view = await mount('zhCN')
  try {
    await until(() => view.current().isSuccess)
    assert.deepEqual(expectedSettlement(settlement(view.current())), {
      settlement_amount: '23.73',
      settlement_currency: 'USD',
    })
  } finally {
    await view.close()
  }
})

test('the QueryClient AbortSignal reaches the HTTP GET and aborts on unmount', async () => {
  const calls = observeQuotes(false, true)
  const view = await mount('zhCN')
  await until(() => calls.length === 1)
  const signal = calls[0].signal
  assert.ok(signal instanceof AbortSignal)
  assert.equal(signal.aborted, false)
  await view.close()
  assert.equal(signal.aborted, true)
})
