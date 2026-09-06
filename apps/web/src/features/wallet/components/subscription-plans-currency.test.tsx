/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'

import type { PlanRecord } from '@/features/subscriptions/types'

const domWindow = new Window({ url: 'https://console.example.test/wallet' })
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
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
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
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useSystemConfigStore } = await import('@/stores/system-config-store')
const { SubscriptionPlansCard } = await import('./subscription-plans-card')

const originalGet = api.get
const originalConfig = useSystemConfigStore.getState().config
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

afterEach(() => {
  api.get = originalGet
  useSystemConfigStore.setState({ config: originalConfig })
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('subscription list price currency', () => {
  for (const currency of ['CNY', 'USD'] as const) {
    test(`keeps ${currency} list pricing consistent through checkout despite the platform display currency`, async () => {
      const plan: PlanRecord = {
        payment_methods: ['stripe'],
        plan: {
          id: 7,
          title: 'Starter',
          price_amount: 49.9,
          currency,
          duration_unit: 'month',
          duration_value: 1,
          quota_reset_period: 'monthly',
          enabled: true,
          sort_order: 1,
          max_purchase_per_user: 0,
          total_amount: 100,
          stripe_price_id: 'price_starter',
          allow_balance_pay: false,
          allow_wallet_overflow: true,
        },
      }
      useSystemConfigStore.setState((state) => ({
        config: {
          ...state.config,
          currency: {
            ...state.config.currency,
            quotaDisplayType: currency === 'CNY' ? 'USD' : 'CNY',
            usdExchangeRate: 7,
          },
        },
      }))
      api.get = (async (url) => {
        if (url === '/api/subscription/plans') {
          return { data: { success: true, data: [plan] } }
        }
        if (url === '/api/subscription/self') {
          return {
            data: {
              success: true,
              data: {
                billing_preference: 'wallet_first',
                subscriptions: [],
                all_subscriptions: [],
              },
            },
          }
        }
        assert.equal(url, '/api/subscription/self/reset-vouchers')
        return { data: { success: true, data: [] } }
      }) as typeof api.get

      const container = document.createElement('div')
      document.body.append(container)
      const root = createRoot(container)
      const client = new QueryClient({
        defaultOptions: { queries: { retry: false } },
      })
      try {
        await act(async () => {
          root.render(
            <QueryClientProvider client={client}>
              <I18nextProvider i18n={i18n}>
                <SubscriptionPlansCard topupInfo={null} />
              </I18nextProvider>
            </QueryClientProvider>
          )
        })
        const expectedPrice = `49.9 ${currency}`
        assert.ok(container.textContent?.includes(expectedPrice))
        assert.equal(document.querySelector('[role="dialog"]'), null)

        const subscribeButton = [...container.querySelectorAll('button')].find(
          (button) => button.textContent?.trim() === 'Subscribe Now'
        )
        assert.ok(subscribeButton)
        assert.equal(subscribeButton.disabled, false)
        await act(async () => subscribeButton.click())

        const dialog = document.querySelector('[role="dialog"]')
        assert.ok(dialog)
        assert.ok(dialog.textContent?.includes(expectedPrice))
        assert.equal(
          dialog.textContent?.includes(
            `49.9 ${currency === 'CNY' ? 'USD' : 'CNY'}`
          ),
          false
        )
      } finally {
        await act(async () => root.unmount())
        client.clear()
        container.remove()
      }
    })
  }
})
