/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import type { AmountRequest } from './types'

const domWindow = new Window({
  url: 'https://console.example.test/wallet?discount_code=SAVE',
})
domWindow.document.write('<!doctype html><html><body></body></html>')
Object.defineProperty(domWindow.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
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
  'matchMedia',
  'customElements',
  'CSSStyleSheet',
  'localStorage',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const { act, useEffect } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { Wallet } = await import('./index')
const { usePayment } = await import('./hooks/use-payment')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')

const i18n = createInstance()
await i18n.use(initReactI18next).init({ lng: 'en', resources: {} })
const originalGet = api.get
const originalPost = api.post
const mounted: Array<{
  root: ReturnType<typeof createRoot>
  container: HTMLDivElement
}> = []

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((accept) => {
    resolve = accept
  })
  return { promise, resolve }
}

async function render(node: React.ReactNode) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  mounted.push({ root, container })
  await act(async () => {
    root.render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>)
  })
  return container
}

afterEach(async () => {
  for (const { root, container } of mounted.splice(0)) {
    await act(async () => root.unmount())
    container.remove()
  }
  api.get = originalGet
  api.post = originalPost
  useAuthStore.getState().auth.reset('complete')
})
after(() => domWindow.close())

test('a superseded quote cannot approve checkout while the latest quote is pending', async () => {
  let payment!: ReturnType<typeof usePayment>
  function Probe() {
    const state = usePayment()
    useEffect(() => {
      payment = state
    }, [state])
    return null
  }
  const first = deferred<{ data: { message: string; data: string } }>()
  const second = deferred<{ data: { message: string; data: string } }>()
  api.post = ((_url, request) =>
    (request as AmountRequest).amount === 10
      ? first.promise
      : second.promise) as typeof api.post
  await render(<Probe />)
  let firstResult!: Promise<number>
  let secondResult!: Promise<number>
  await act(async () => {
    firstResult = payment.calculatePaymentAmount(10, 'alipay')
    secondResult = payment.calculatePaymentAmount(100, 'alipay')
  })
  await act(async () => {
    first.resolve({ data: { message: 'success', data: '10.00' } })
    assert.equal(await firstResult, 0)
  })
  assert.equal(payment.amount, 0)
  assert.equal(payment.calculating, true)
  await act(async () => {
    second.resolve({ data: { message: 'success', data: '100.00' } })
    assert.equal(await secondResult, 100)
  })
  assert.equal(payment.amount, 100)
  assert.equal(payment.calculating, false)
})

test('late discount validation cannot quote an old amount into a new checkout', async () => {
  const user = {
    id: 7,
    username: 'checkout-user',
    role: 1,
    developer_access_granted: false,
  }
  useAuthStore.getState().auth.setUser(user)
  api.get = (async (url) => ({
    data: {
      success: true,
      data:
        url === '/api/user/topup/info'
          ? {
              enable_online_topup: true,
              enable_stripe_topup: false,
              pay_methods: [
                {
                  name: 'Alipay',
                  type: 'alipay',
                  settlement_currency: 'CNY',
                  platform_units_per_usd: '7',
                  settlement_units_per_usd: '7',
                },
              ],
              min_topup: 10,
              stripe_min_topup: 10,
              amount_options: [10, 100],
              discount: {},
            }
          : url === '/api/user/self'
            ? user
            : {},
    },
  })) as typeof api.get
  const validation = deferred<{
    data: {
      success: boolean
      data: { code: string; discount_percent: number; min_amount: number }
    }
  }>()
  const checkoutQuote = deferred<{
    data: { message: string; data: string }
  }>()
  const quotes: AmountRequest[] = []
  let requestedCheckout = false
  api.post = (async (url, request) => {
    if (url === '/api/user/discount-code/validate') return validation.promise
    assert.equal(url, '/api/user/amount')
    const quoteRequest = request as AmountRequest
    quotes.push(quoteRequest)
    if (requestedCheckout) return checkoutQuote.promise
    return { data: { message: 'success', data: String(quoteRequest.amount) } }
  }) as typeof api.post
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const container = await render(
    <QueryClientProvider client={queryClient}>
      <Wallet />
    </QueryClientProvider>
  )
  const largerPreset = container.querySelector<HTMLButtonElement>(
    'button[aria-label^="Preset amount: 100 "]'
  )
  assert.ok(largerPreset)
  await act(async () => largerPreset.click())
  requestedCheckout = true
  const pay = container.querySelector<HTMLButtonElement>(
    'button[aria-label="Payment option 1"]'
  )
  assert.ok(pay)
  await act(async () => pay.click())
  const requestsBeforeValidation = quotes.length
  await act(async () => {
    validation.resolve({
      data: {
        success: true,
        data: { code: 'SAVE', discount_percent: 10, min_amount: 10 },
      },
    })
  })
  assert.equal(quotes.length, requestsBeforeValidation)
  assert.equal(document.querySelector('[role="alertdialog"]'), null)
  await act(async () => {
    checkoutQuote.resolve({ data: { message: 'success', data: '100.00' } })
  })
  const confirmation = document.querySelector('[role="alertdialog"]')
  assert.ok(confirmation)
  assert.ok(confirmation.textContent?.includes('100 (Platform)'))
  assert.ok(confirmation.textContent?.includes('100 CNY'))
  assert.equal(quotes.at(-1)?.amount, 100)
  assert.equal(quotes.at(-1)?.discount_code, undefined)
  queryClient.clear()
})
