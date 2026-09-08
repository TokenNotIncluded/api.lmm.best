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
  window.history.replaceState({}, '', '/wallet?discount_code=SAVE')
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
  const validations: Array<ReturnType<typeof deferred<ValidationResult>>> = []
  const checkoutQuote = deferred<{ data: { message: string; data: string } }>()
  const quotes: AmountRequest[] = []
  api.post = (async (url, request) => {
    if (url === '/api/user/discount-code/validate') {
      const response = deferred<ValidationResult>()
      validations.push(response)
      return response.promise
    }
    assert.equal(url, '/api/user/amount')
    const quote = request as AmountRequest
    quotes.push(quote)
    return quote.discount_code
      ? checkoutQuote.promise
      : { data: { message: 'success', data: String(quote.amount) } }
  }) as typeof api.post
  const { container, queryClient } = await renderWallet()
  const largerPreset = container.querySelector<HTMLButtonElement>(
    'button[aria-label^="Preset amount: 100 "]'
  )
  assert.ok(largerPreset)
  await act(async () => largerPreset.click())
  const pay = container.querySelector<HTMLButtonElement>(
    'button[aria-label="Payment option 1"]'
  )
  assert.ok(pay)
  await act(async () => pay.click())
  assert.equal(validations.length, 3)
  const requestsBeforeValidation = quotes.length
  await act(async () => {
    validations[0].resolve(validDiscount)
    validations[1].resolve(validDiscount)
  })
  assert.equal(quotes.length, requestsBeforeValidation)
  assert.equal(document.querySelector('[role="alertdialog"]'), null)
  await act(async () => validations[2].resolve(validDiscount))
  assert.deepEqual(quotes.at(-1), {
    amount: 100,
    payment_method: 'alipay',
    discount_code: 'SAVE',
  })
  assert.equal(document.querySelector('[role="alertdialog"]'), null)
  await act(async () =>
    checkoutQuote.resolve({ data: { message: 'success', data: '90.00' } })
  )
  const confirmation = document.querySelector('[role="alertdialog"]')
  assert.ok(confirmation)
  assert.ok(confirmation.textContent?.includes('100 (Platform)'))
  assert.ok(confirmation.textContent?.includes('90 CNY'))
  queryClient.clear()
})

async function renderWallet(activated = false) {
  const user = {
    id: 7,
    username: 'checkout-user',
    role: 1,
    developer_access_granted: activated,
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
              pay_methods: ['alipay', 'wxpay'].map((type) => ({
                name: type,
                type,
                settlement_currency: 'CNY',
                platform_units_per_usd: '7',
                settlement_units_per_usd: '7',
              })),
              min_topup: 10,
              stripe_min_topup: 10,
              amount_options: [10, 100],
              discount: {},
            }
          : url === '/api/user/self'
            ? user
            : url === '/api/user/aff'
              ? ''
              : [],
    },
  })) as typeof api.get
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const container = await render(
    <QueryClientProvider client={queryClient}>
      <Wallet />
    </QueryClientProvider>
  )
  return { container, queryClient }
}

type ValidationResult = {
  data: {
    success: boolean
    data?: { code: string; discount_percent: number; min_amount: number }
  }
}
const validDiscount = {
  data: {
    success: true,
    data: { code: 'SAVE', discount_percent: 10, min_amount: 10 },
  },
}

test('a checkout link revalidates a changed payment method and waits for its discounted quote', async () => {
  window.history.replaceState({}, '', '/wallet?discount_code=save')
  const validations: Array<{
    request: { code: string; amount: number; payment_method: string }
    response: ReturnType<typeof deferred<ValidationResult>>
  }> = []
  const discountedQuote = deferred<{
    data: { message: string; data: string }
  }>()
  const quotes: AmountRequest[] = []
  api.post = (async (url, request) => {
    if (url === '/api/user/discount-code/validate') {
      const response = deferred<ValidationResult>()
      validations.push({
        request: request as (typeof validations)[number]['request'],
        response,
      })
      return response.promise
    }
    assert.equal(url, '/api/user/amount')
    const quote = request as AmountRequest
    quotes.push(quote)
    return quote.discount_code
      ? discountedQuote.promise
      : { data: { message: 'success', data: String(quote.amount) } }
  }) as typeof api.post
  const { container, queryClient } = await renderWallet()
  assert.equal(validations.length, 1)
  assert.equal(validations[0].request.payment_method, 'alipay')
  const pay = container.querySelector<HTMLButtonElement>(
    'button[aria-label="Payment option 2"]'
  )
  assert.ok(pay)
  await act(async () => pay.click())
  assert.equal(
    validations.length,
    2,
    'the same amount needs a fresh validation for wxpay'
  )
  assert.deepEqual(validations[1].request, {
    code: 'save',
    amount: 10,
    payment_method: 'wxpay',
  })
  await act(async () => validations[0].response.resolve(validDiscount))
  assert.equal(quotes.filter((quote) => quote.discount_code).length, 0)
  assert.equal(document.querySelector('[role="alertdialog"]'), null)
  await act(async () => validations[1].response.resolve(validDiscount))
  assert.deepEqual(quotes.at(-1), {
    amount: 10,
    payment_method: 'wxpay',
    discount_code: 'SAVE',
  })
  assert.equal(document.querySelector('[role="alertdialog"]'), null)
  await act(async () =>
    discountedQuote.resolve({ data: { message: 'success', data: '9.00' } })
  )
  const confirmation = document.querySelector('[role="alertdialog"]')
  assert.ok(confirmation)
  assert.ok(confirmation.textContent?.includes('9 CNY'))
  assert.equal(
    validations.length,
    2,
    'completed validation must not trigger another effect'
  )
  queryClient.clear()
})

test('a failed checkout-link discount unlocks manual retry without an automatic retry loop', async () => {
  const first = deferred<ValidationResult>()
  let validations = 0
  api.post = (async (url, request) => {
    if (url === '/api/user/discount-code/validate') {
      validations++
      return validations === 1 ? first.promise : validDiscount
    }
    assert.equal(url, '/api/user/amount')
    return {
      data: {
        message: 'success',
        data: (request as AmountRequest).discount_code ? '9' : '10',
      },
    }
  }) as typeof api.post
  const { container, queryClient } = await renderWallet(true)
  const input = container.querySelector<HTMLInputElement>('#discount-code')
  assert.ok(input)
  assert.equal(input.readOnly, true)
  await act(async () => first.resolve({ data: { success: false } }))
  assert.equal(
    input.readOnly,
    false,
    'an invalid link must let the customer retry or edit the code'
  )
  const apply = Array.from(
    container.querySelectorAll<HTMLButtonElement>('button')
  ).find((button) => button.textContent?.trim() === 'Apply')
  assert.ok(apply)
  assert.equal(apply.disabled, false)
  assert.equal(validations, 1)
  await act(async () => apply.click())
  assert.equal(validations, 2)
  assert.ok(container.textContent?.includes('Discount applied: 10% off'))
  queryClient.clear()
})

test('editing a pending manual discount prevents the old code from approving a quote', async () => {
  window.history.replaceState({}, '', '/wallet')
  const oldValidation = deferred<ValidationResult>()
  const quotes: AmountRequest[] = []
  api.post = (async (url, request) => {
    if (url === '/api/user/discount-code/validate') {
      return (request as { code: string }).code === 'SAVE'
        ? oldValidation.promise
        : {
            data: {
              success: true,
              data: { code: 'OTHER', discount_percent: 20, min_amount: 10 },
            },
          }
    }
    assert.equal(url, '/api/user/amount')
    const quote = request as AmountRequest
    quotes.push(quote)
    return {
      data: { message: 'success', data: quote.discount_code ? '8' : '10' },
    }
  }) as typeof api.post
  const { container, queryClient } = await renderWallet(true)
  const input = container.querySelector<HTMLInputElement>('#discount-code')
  assert.ok(input)
  const setInputValue = Object.getOwnPropertyDescriptor(
    domWindow.HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(setInputValue)
  const changeCode = async (code: string) => {
    await act(async () => {
      setInputValue.call(input, code)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
  }
  const apply = Array.from(
    container.querySelectorAll<HTMLButtonElement>('button')
  ).find((button) => button.textContent?.trim() === 'Apply')
  assert.ok(apply)
  await changeCode('SAVE')
  await act(async () => apply.click())
  assert.equal(apply.disabled, true)
  await changeCode('OTHER')
  await act(async () => oldValidation.resolve(validDiscount))
  assert.equal(input.value, 'OTHER')
  assert.equal(quotes.filter((quote) => quote.discount_code).length, 0)
  assert.equal(document.querySelector('[role="alertdialog"]'), null)
  await act(async () => apply.click())
  assert.equal(quotes.at(-1)?.discount_code, 'OTHER')
  assert.ok(container.textContent?.includes('Discount applied: 20% off'))
  queryClient.clear()
})

test('a failed discounted quote unlocks the checkout-link code for retry', async () => {
  const quote = deferred<{ data: { success: boolean } }>()
  let validations = 0
  api.post = (async (url, request) => {
    if (url === '/api/user/discount-code/validate') {
      validations++
      return validDiscount
    }
    assert.equal(url, '/api/user/amount')
    return (request as AmountRequest).discount_code
      ? quote.promise
      : { data: { message: 'success', data: '10' } }
  }) as typeof api.post
  const { container, queryClient } = await renderWallet(true)
  await act(async () => quote.resolve({ data: { success: false } }))
  const input = container.querySelector<HTMLInputElement>('#discount-code')
  assert.ok(input)
  assert.equal(input.readOnly, false)
  assert.equal(validations, 1)
  assert.equal(
    container.textContent?.includes('Discount applied: 10% off'),
    false
  )
  assert.equal(document.querySelector('[role="alertdialog"]'), null)
  queryClient.clear()
})
