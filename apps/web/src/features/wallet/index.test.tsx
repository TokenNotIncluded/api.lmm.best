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

import type { AuthUser } from '@/stores/auth-store'

import type { AmountRequest, TopupInfo } from './types'

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
const { PaymentConfirmDialog } =
  await import('./components/dialogs/payment-confirm-dialog')
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

async function renderWallet(
  activated = false,
  options: {
    topupInfo?: Partial<TopupInfo>
    setting?: AuthUser['setting']
  } = {}
) {
  const user = {
    id: 7,
    username: 'checkout-user',
    role: 1,
    developer_access_granted: activated,
    setting: options.setting,
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
              ...options.topupInfo,
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

test('a failed checkout-link discount automatically revalidates when the user selects a qualifying amount', async () => {
  window.history.replaceState({}, '', '/wallet?discount_code=SAVE')
  const validationRequests: Array<{ code: string; amount: number }> = []
  api.post = (async (url, request) => {
    if (url === '/api/user/discount-code/validate') {
      const body = request as { code: string; amount: number }
      validationRequests.push({ code: body.code, amount: body.amount })
      if (body.amount < 50) {
        return {
          data: {
            success: false,
            message: 'Amount does not meet discount requirements',
          },
        }
      }
      return {
        data: {
          success: true,
          data: { code: 'SAVE', discount_percent: 40, min_amount: 50 },
        },
      }
    }
    assert.equal(url, '/api/user/amount')
    const req = request as AmountRequest
    return {
      data: {
        message: 'success',
        data: req.discount_code ? String(req.amount * 0.6) : String(req.amount),
      },
    }
  }) as typeof api.post

  const { container, queryClient } = await renderWallet(true, {
    topupInfo: {
      amount_options: [10, 100],
      min_topup: 10,
    },
  })

  assert.equal(validationRequests.length, 1)
  assert.equal(validationRequests[0].amount, 10)

  const preset100 = Array.from(
    container.querySelectorAll<HTMLButtonElement>('button')
  ).find((btn) => btn.getAttribute('aria-label')?.includes('100'))
  assert.ok(preset100, 'preset 100 button should exist')
  await act(async () => preset100.click())

  assert.equal(validationRequests.length, 2)
  assert.equal(validationRequests[1].amount, 100)
  assert.equal(validationRequests[1].code, 'SAVE')
  assert.ok(container.textContent?.includes('Discount applied: 40% off'))
  queryClient.clear()
})

test('an unactivated account retains access to the discount code input when configurable topup is enabled', async () => {
  window.history.replaceState({}, '', '/wallet')
  const { container, queryClient } = await renderWallet(false)
  const input = container.querySelector<HTMLInputElement>('#discount-code')
  assert.ok(
    input,
    'discount code input should be available even in neutralMode'
  )
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

const pancakeTopup = {
  enable_online_topup: false,
  enable_waffo_pancake_topup: true,
  waffo_pancake_min_topup: 10,
  pay_methods: [
    {
      name: 'Pancake',
      type: 'waffo_pancake',
      settlement_currency: 'USD',
      platform_units_per_usd: '7',
      settlement_units_per_usd: '1',
    },
  ],
} satisfies Partial<TopupInfo>
const quoteResponse = (amount: string, currency: 'CNY' | 'USD' = 'CNY') => ({
  data: { message: 'success', data: amount, settlement_currency: currency },
})
const paymentButton = () => {
  const button = document.querySelector<HTMLButtonElement>(
    'button[aria-label="Payment option 1"]'
  )
  assert.ok(button)
  return button
}
const confirmButton = () => {
  const dialog = document.querySelector('[role="alertdialog"]')
  assert.ok(dialog)
  const button = Array.from(
    dialog.querySelectorAll<HTMLButtonElement>('button')
  ).find((item) => item.textContent?.trim() === 'Confirm Payment')
  assert.ok(button)
  return button
}

for (const currency of ['CNY', 'USD'] as const) {
  test(`Pancake shows and sends the exact ${currency} server quote, not the legacy coupon fiat preview`, async () => {
    const requests: Array<{ url: unknown; body: unknown }> = []
    api.post = (async (url, body) => {
      requests.push({ url, body })
      if (url === '/api/user/discount-code/validate') {
        return {
          data: {
            ...validDiscount.data,
            data: {
              ...validDiscount.data.data,
              pay_amount: 999,
              settlement_currency: 'USD',
            },
          },
        }
      }
      if (url === '/api/user/waffo-pancake/amount') {
        return quoteResponse(
          (body as AmountRequest).discount_code ? '63.0700' : '70.00',
          currency
        )
      }
      assert.equal(url, '/api/user/waffo-pancake/pay')
      return { data: { success: false, message: 'mock checkout rejected' } }
    }) as typeof api.post
    const { container, queryClient } = await renderWallet(false, {
      topupInfo: pancakeTopup,
    })
    assert.ok(container.textContent?.includes(`63.0700 ${currency}`))
    assert.equal(container.textContent?.includes('1 USD / 7'), false)
    assert.equal(container.textContent?.includes('999'), false)
    await act(async () => paymentButton().click())
    const dialog = document.querySelector('[role="alertdialog"]')
    assert.ok(dialog?.textContent?.includes(`63.0700 ${currency}`))
    assert.ok(dialog?.textContent?.includes('10 (Platform)'))
    await act(async () => confirmButton().click())
    assert.deepEqual(requests.at(-1), {
      url: '/api/user/waffo-pancake/pay',
      body: {
        amount: 10,
        settlement_amount: '63.0700',
        settlement_currency: currency,
        checkout_region: 'global',
        checkout_language: 'en',
        discount_code: 'SAVE',
      },
    })
    assert.ok(
      requests.some(
        ({ url, body }) =>
          url === '/api/user/waffo-pancake/amount' &&
          (body as AmountRequest).discount_code === 'SAVE'
      )
    )
    assert.ok(container.textContent?.includes('Payment request failed'))
    assert.equal(container.textContent?.includes('Payment page opened'), false)
    queryClient.clear()
  })
}

test('an incomplete Pancake quote stays visibly unavailable and cannot open confirmation', async () => {
  window.history.replaceState({}, '', '/wallet')
  api.post = (async (url) => {
    assert.equal(url, '/api/user/waffo-pancake/amount')
    return { data: { message: 'success', data: '70.00' } }
  }) as typeof api.post
  const { container, queryClient } = await renderWallet(false, {
    topupInfo: pancakeTopup,
  })
  assert.ok(container.textContent?.includes('Payment unavailable'))
  await act(async () => paymentButton().click())
  assert.equal(document.querySelector('[role="alertdialog"]'), null)
  assert.ok(container.textContent?.includes('Payment unavailable'))
  queryClient.clear()
})

test('SETTLEMENT_QUOTE_CHANGED only refreshes and requires a new selection and confirmation', async () => {
  window.history.replaceState({}, '', '/wallet')
  const refreshed = deferred<ReturnType<typeof quoteResponse>>()
  let quotes = 0
  let payments = 0
  api.post = (async (url, body) => {
    if (url === '/api/user/waffo-pancake/amount') {
      quotes++
      return quotes === 3
        ? refreshed.promise
        : quoteResponse(quotes < 3 ? '70.00' : '71.0000')
    }
    assert.equal(url, '/api/user/waffo-pancake/pay')
    payments++
    assert.equal(
      (body as { settlement_amount: string }).settlement_amount,
      payments === 1 ? '70.00' : '71.0000'
    )
    return { data: { success: false, code: 'SETTLEMENT_QUOTE_CHANGED' } }
  }) as typeof api.post
  const { container, queryClient } = await renderWallet(false, {
    topupInfo: pancakeTopup,
  })
  await act(async () => paymentButton().click())
  await act(async () => confirmButton().click())
  assert.equal(payments, 1)
  assert.equal(quotes, 3)
  assert.equal(document.querySelector('[role="alertdialog"]'), null)
  assert.ok(
    container.textContent?.includes(
      'The payment quote changed. Review the updated amount and confirm again.'
    )
  )
  assert.equal(container.textContent?.includes('70.00 CNY'), false)
  await act(async () => refreshed.resolve(quoteResponse('71.0000')))
  assert.equal(payments, 1, 'refresh must never submit a new payment')
  assert.equal(document.querySelector('[role="alertdialog"]'), null)
  assert.ok(container.textContent?.includes('71.0000 CNY'))
  await act(async () => paymentButton().click())
  assert.equal(payments, 1)
  await act(async () => confirmButton().click())
  assert.equal(payments, 2)
  queryClient.clear()
})

test('changing the amount invalidates confirmation and isolates late Pancake quotes', async () => {
  window.history.replaceState({}, '', '/wallet')
  const pending = deferred<ReturnType<typeof quoteResponse>>()
  let requests = 0
  api.post = (async (url, body) => {
    assert.equal(url, '/api/user/waffo-pancake/amount')
    requests++
    return requests === 2
      ? pending.promise
      : quoteResponse(
          (body as AmountRequest).amount === 100 ? '640.0000' : '64.00'
        )
  }) as typeof api.post
  const { container, queryClient } = await renderWallet(false, {
    topupInfo: pancakeTopup,
  })
  await act(async () => paymentButton().click())
  const preset = container.querySelector<HTMLButtonElement>(
    'button[aria-label^="Preset amount: 100 "]'
  )
  assert.ok(preset)
  await act(async () => preset.click())
  await act(async () => pending.resolve(quoteResponse('64.00')))
  assert.equal(document.querySelector('[role="alertdialog"]'), null)
  assert.ok(container.textContent?.includes('640.0000 CNY'))
  assert.equal(container.textContent?.includes('64.00 CNY'), false)
  await act(async () => paymentButton().click())
  assert.ok(
    document
      .querySelector('[role="alertdialog"]')
      ?.textContent?.includes('640.0000 CNY')
  )
  queryClient.clear()
})

for (const currency of ['CNY', 'USD'] as const) {
  test(`confirmation uses the exact ${currency} quote instead of stale numeric amount and metadata`, async () => {
    await render(
      <PaymentConfirmDialog
        open
        onOpenChange={() => undefined}
        onConfirm={() => assert.fail('rendering must not submit')}
        topupAmount={100}
        paymentAmount={999}
        settlementQuote={{ amount: '12.3400', currency }}
        paymentMethod={pancakeTopup.pay_methods[0]}
        calculating={false}
        processing={false}
        discountRate={0.5}
        discountPercent={50}
        discountCode='SAVE'
      />
    )
    const text =
      document.querySelector('[role="alertdialog"]')?.textContent ?? ''
    assert.ok(text.includes(`12.3400 ${currency}`))
    assert.ok(text.includes('100 (Platform)'))
    assert.equal(text.includes('999'), false)
    assert.equal(confirmButton().disabled, false)
  })
}

for (const state of ['missing', 'invalid', 'pending', 'processing'] as const) {
  test(`a ${state} Pancake confirmation cannot submit`, async () => {
    await render(
      <PaymentConfirmDialog
        open
        onOpenChange={() => undefined}
        onConfirm={() =>
          assert.fail('unavailable confirmation must not submit')
        }
        topupAmount={100}
        paymentAmount={999}
        settlementQuote={
          state === 'missing'
            ? null
            : { amount: state === 'invalid' ? '0' : '12.3400', currency: 'CNY' }
        }
        paymentMethod={pancakeTopup.pay_methods[0]}
        calculating={state === 'pending'}
        processing={state === 'processing'}
      />
    )
    assert.equal(confirmButton().disabled, true)
    await act(async () => confirmButton().click())
    const text =
      document.querySelector('[role="alertdialog"]')?.textContent ?? ''
    assert.equal(text.includes('999'), false)
    if (state === 'missing' || state === 'invalid') {
      assert.ok(text.includes('Payment unavailable'))
    }
    if (state === 'pending') assert.equal(text.includes('12.3400 CNY'), false)
  })
}

test('fixed gateway confirmation ignores the unrelated Pancake settlement quote', async () => {
  await render(
    <PaymentConfirmDialog
      open
      onOpenChange={() => undefined}
      onConfirm={() => undefined}
      topupAmount={100}
      paymentAmount={70}
      settlementQuote={{ amount: '12.3400', currency: 'USD' }}
      paymentMethod={{
        name: 'Alipay',
        type: 'alipay',
        settlement_unit: 'CNY',
        unit_price: '0.7',
      }}
      calculating={false}
      processing={false}
    />
  )
  const text = document.querySelector('[role="alertdialog"]')?.textContent ?? ''
  assert.ok(text.includes('70 CNY'))
  assert.equal(text.includes('12.3400'), false)
  assert.equal(text.includes('USD'), false)
})

test('editing a coupon invalidates its pending Pancake quote before the coupon response can approve it', async () => {
  window.history.replaceState({}, '', '/wallet')
  const pending = deferred<ReturnType<typeof quoteResponse>>()
  api.post = (async (url, body) => {
    if (url === '/api/user/discount-code/validate') return validDiscount
    assert.equal(url, '/api/user/waffo-pancake/amount')
    return (body as AmountRequest).discount_code
      ? pending.promise
      : quoteResponse('70.00')
  }) as typeof api.post
  const { container, queryClient } = await renderWallet(true, {
    topupInfo: pancakeTopup,
  })
  const input = container.querySelector<HTMLInputElement>('#discount-code')
  assert.ok(input)
  const setValue = Object.getOwnPropertyDescriptor(
    domWindow.HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(setValue)
  const changeCode = (code: string) =>
    act(async () => {
      setValue.call(input, code)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
  await changeCode('SAVE')
  const apply = Array.from(
    container.querySelectorAll<HTMLButtonElement>('button')
  ).find((button) => button.textContent?.trim() === 'Apply')
  assert.ok(apply)
  await act(async () => apply.click())
  await changeCode('OTHER')
  await act(async () => pending.resolve(quoteResponse('63.0000')))
  assert.equal(input.value, 'OTHER')
  assert.equal(container.textContent?.includes('63.0000 CNY'), false)
  assert.ok(container.textContent?.includes('Payment unavailable'))
  assert.equal(document.querySelector('[role="alertdialog"]'), null)
  queryClient.clear()
})

const scopeChanges = {
  account: () => {
    const { auth } = useAuthStore.getState()
    assert.ok(auth.user)
    auth.setUser({ ...auth.user, id: 8 })
  },
  currency: () => {
    const { auth } = useAuthStore.getState()
    assert.ok(auth.user)
    auth.setUser({
      ...auth.user,
      setting: { settlement_currency: 'CNY' },
    })
  },
  session: () =>
    useAuthStore.setState((state) => ({
      auth: {
        ...state.auth,
        session: {
          sid: 'replacement',
          current: true,
          login_method: 'password',
          ip: '',
          user_agent: '',
          created_at: 1,
          last_active_at: 1,
          expires_at: 9999999999,
        },
      },
    })),
}
for (const [scope, changeScope] of Object.entries(scopeChanges)) {
  test(`a ${scope} change hides confirmation and isolates the late quote`, async () => {
    window.history.replaceState({}, '', '/wallet')
    const pending = deferred<ReturnType<typeof quoteResponse>>()
    let requests = 0
    api.post = (async (url) => {
      assert.equal(url, '/api/user/waffo-pancake/amount')
      requests++
      return requests === 2
        ? pending.promise
        : quoteResponse(requests < 3 ? '10.000' : '80.000')
    }) as typeof api.post
    const { container, queryClient } = await renderWallet(false, {
      topupInfo: pancakeTopup,
      setting: { settlement_currency: 'USD' },
    })
    await act(async () => paymentButton().click())
    await act(async () => changeScope())
    await act(async () => pending.resolve(quoteResponse('10.000')))
    assert.equal(document.querySelector('[role="alertdialog"]'), null)
    assert.equal(container.textContent?.includes('10.000 CNY'), false)
    assert.ok(container.textContent?.includes('80.000 CNY'))
    queryClient.clear()
  })

  test(`a ${scope} change prevents a late checkout response from redirecting or reporting success`, async () => {
    window.history.replaceState({}, '', '/wallet')
    const pending = deferred<{
      data: { success: boolean; data: { checkout_url: string } }
    }>()
    api.post = (async (url) => {
      if (url === '/api/user/waffo-pancake/amount') {
        return quoteResponse('70.00')
      }
      assert.equal(url, '/api/user/waffo-pancake/pay')
      return pending.promise
    }) as typeof api.post
    const { container, queryClient } = await renderWallet(false, {
      topupInfo: pancakeTopup,
      setting: { settlement_currency: 'USD' },
    })
    await act(async () => paymentButton().click())
    await act(async () => confirmButton().click())
    await act(async () => changeScope())
    await act(async () =>
      pending.resolve({
        data: {
          success: true,
          data: { checkout_url: 'https://checkout.example.test/old-owner' },
        },
      })
    )
    assert.equal(window.location.pathname, '/wallet')
    assert.equal(container.textContent?.includes('Payment page opened'), false)
    assert.equal(document.querySelector('[role="alertdialog"]'), null)
    queryClient.clear()
  })
}

test('proceed to payment preserves selected Waffo sub-method index and dispatches successfully', async () => {
  window.history.replaceState({}, '', '/wallet')
  const waffoPayRequests: Array<{ url: string; body: unknown }> = []
  api.post = (async (url, body) => {
    if (url === '/api/user/waffo/amount') {
      return { data: { message: 'success', data: '10.00' } }
    }
    if (url === '/api/user/waffo/pay') {
      waffoPayRequests.push({ url, body })
      return {
        data: {
          success: true,
          data: { checkout_url: 'https://waffo.example.test/pay' },
        },
      }
    }
    return { data: { message: 'success', data: '10.00' } }
  }) as typeof api.post

  const waffoTopup: Partial<TopupInfo> = {
    enable_online_topup: true,
    enable_waffo_topup: true,
    pay_methods: [
      {
        name: 'alipay',
        type: 'alipay',
        settlement_currency: 'CNY',
        platform_units_per_usd: '7',
        settlement_units_per_usd: '7',
      },
    ],
    waffo_pay_methods: [
      {
        name: 'Waffo Card',
        payMethodType: 'card',
        payMethodName: 'waffo_card',
      },
      {
        name: 'Waffo Crypto',
        payMethodType: 'crypto',
        payMethodName: 'waffo_crypto',
      },
    ],
    min_topup: 10,
    waffo_min_topup: 10,
    amount_options: [10, 100],
  }

  const { container, queryClient } = await renderWallet(true, {
    topupInfo: waffoTopup,
  })

  // Find Waffo Crypto button (index 1)
  const waffoCryptoBtn = Array.from(
    container.querySelectorAll<HTMLButtonElement>('button')
  ).find((btn) => btn.textContent?.includes('Waffo Crypto'))
  assert.ok(waffoCryptoBtn, 'Waffo Crypto button should exist')

  // Click Waffo Crypto (opens confirmation dialog)
  await act(async () => waffoCryptoBtn.click())
  let dialog = document.querySelector('[role="alertdialog"]')
  assert.ok(dialog, 'confirmation dialog should open')

  // Close dialog without submitting
  const cancelBtn = Array.from(
    dialog.querySelectorAll<HTMLButtonElement>('button')
  ).find((b) => b.textContent?.trim() === 'Cancel')
  assert.ok(cancelBtn, 'cancel button should exist')
  await act(async () => cancelBtn.click())
  assert.equal(document.querySelector('[role="alertdialog"]'), null)

  // Now click primary Pay CTA
  const payCta = Array.from(
    container.querySelectorAll<HTMLButtonElement>('button')
  ).find((btn) => btn.textContent?.startsWith('Pay '))
  assert.ok(payCta, 'primary pay CTA should exist')
  await act(async () => payCta.click())

  // Confirmation dialog opens again
  dialog = document.querySelector('[role="alertdialog"]')
  assert.ok(dialog, 'dialog should re-open after clicking primary pay CTA')

  // Confirm payment
  await act(async () => confirmButton().click())

  // Verify that dispatchSelectedPayment dispatched to /api/user/waffo/pay with pay_method_index: 1
  assert.equal(waffoPayRequests.length, 1)
  assert.deepEqual(waffoPayRequests[0].body, {
    amount: 10,
    pay_method_index: 1,
  })
  queryClient.clear()
})

test('manual discount code is not locked as URL discount and can be edited or removed', async () => {
  window.history.replaceState({}, '', '/wallet')
  api.post = (async (url, request) => {
    if (url === '/api/user/discount-code/validate') {
      const code = (request as { code: string }).code
      if (code === 'MANUAL20') {
        return {
          data: {
            success: true,
            data: { code: 'MANUAL20', discount_percent: 20, min_amount: 10 },
          },
        }
      }
      return { data: { success: false, message: 'Invalid code' } }
    }
    assert.equal(url, '/api/user/amount')
    const req = request as AmountRequest
    return {
      data: {
        message: 'success',
        data: req.discount_code ? '8.00' : '10.00',
      },
    }
  }) as typeof api.post

  const { container, queryClient } = await renderWallet(true)
  const input = container.querySelector<HTMLInputElement>('#discount-code')
  assert.ok(input)
  assert.equal(input.readOnly, false)

  // Type MANUAL20
  const setValue = Object.getOwnPropertyDescriptor(
    domWindow.HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(setValue)
  await act(async () => {
    setValue.call(input, 'MANUAL20')
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })

  const applyBtn = Array.from(
    container.querySelectorAll<HTMLButtonElement>('button')
  ).find((b) => b.textContent?.trim() === 'Apply')
  assert.ok(applyBtn)
  await act(async () => applyBtn.click())

  // Discount is applied
  assert.ok(container.textContent?.includes('Discount applied: 20% off'))
  // Input is STILL NOT readOnly
  assert.equal(
    input.readOnly,
    false,
    'manual discount should not lock input as readOnly'
  )
  // Label does not say "Discount code from URL"
  assert.equal(container.textContent?.includes('Discount code from URL'), false)
  assert.equal(
    container.textContent?.includes(
      'This code came from the checkout link and cannot be edited.'
    ),
    false
  )

  // "Remove" button appears
  const removeBtn = Array.from(
    container.querySelectorAll<HTMLButtonElement>('button')
  ).find((b) => b.textContent?.trim() === 'Remove')
  assert.ok(removeBtn, 'Remove button should exist for applied discount')

  // Click Remove
  await act(async () => removeBtn.click())

  // Discount is cleared
  assert.equal(input.value, '')
  assert.equal(
    container.textContent?.includes('Discount applied: 20% off'),
    false
  )
  queryClient.clear()
})

test('invalid URL discount code reverts to regular price and allows checkout without error', async () => {
  window.history.replaceState({}, '', '/wallet?discount_code=EXPIRED')
  let validationCalled = 0
  const validation = deferred<{ data: { success: boolean; message: string } }>()
  const quotes: AmountRequest[] = []
  api.post = (async (url, request) => {
    if (url === '/api/user/discount-code/validate') {
      validationCalled++
      return validation.promise
    }
    assert.equal(url, '/api/user/amount')
    const req = request as AmountRequest
    quotes.push(req)
    return {
      data: {
        message: 'success',
        data: '10.00',
      },
    }
  }) as typeof api.post

  const { container, queryClient } = await renderWallet(true)
  assert.equal(validationCalled, 1)

  // Resolve validation as expired/failed
  await act(async () =>
    validation.resolve({
      data: {
        success: false,
        message: 'The coupon code has expired',
      },
    })
  )

  // UI reverted to regular price (no discount applied)
  assert.equal(container.textContent?.includes('Discount applied'), false)

  const payCta = Array.from(
    container.querySelectorAll<HTMLButtonElement>('button')
  ).find((btn) => btn.textContent?.startsWith('Pay '))
  assert.ok(payCta, 'Pay CTA should exist')
  await act(async () => payCta.click())

  // Confirmation dialog opens successfully at regular price
  const dialog = document.querySelector('[role="alertdialog"]')
  assert.ok(
    dialog,
    'confirmation dialog must open successfully for regular price'
  )
  assert.ok(dialog.textContent?.includes('10 CNY'))

  // Validation was NOT re-called with the dead code during checkout
  assert.equal(
    validationCalled,
    1,
    'checkout must not re-validate the invalid code'
  )
  // Last quote did not have discount_code
  assert.equal(quotes.at(-1)?.discount_code, undefined)

  queryClient.clear()
})
