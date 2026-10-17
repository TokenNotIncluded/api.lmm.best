/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

const window = new Window({ url: 'https://console.example.test/wallet' })
for (const key of [
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
    value: window[key],
  })
}
Object.defineProperty(globalThis, 'window', {
  configurable: true,
  value: window,
})
Object.defineProperty(window.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { RechargeFormCard } = await import('./recharge-form-card')
const { PaymentCurrencyContext } = await import('../hooks/use-payment-currency')
const { walletCatalog } = await import('../lib/wallet-fixtures.test-support')
const { useSystemConfigStore } = await import('@/stores/system-config-store')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})
const originalConfig = useSystemConfigStore.getState().config
useSystemConfigStore.setState((state) => ({
  config: {
    ...state.config,
    currency: {
      ...state.config.currency,
      currencyUnit: 'credit',
      creditsPerUsd: 500000,
      creditsPerUsdExact: '500000',
      cnyPerUsd: 7,
      cnyPerUsdExact: '7',
    },
  },
}))

const method = {
  name: 'Test card',
  type: 'alipay',
  min_topup_credit: 5000000,
  settlement_unit: 'USD',
  unit_price: 1,
}
const topupInfo = walletCatalog({
  credit_metadata_version: 1,
  credit_min_topup: 5000000,
  credit_amount_options: [5000000, 10000000],
  credit_discount: {},
  enable_online_topup: true,
  enable_stripe_topup: false,
  min_topup: 5000000,
  stripe_min_topup: 5000000,
  amount_options: [],
  discount: {},
  pay_methods: [method],
})
const noop = () => {}
const defaults: React.ComponentProps<typeof RechargeFormCard> = {
  topupInfo,
  presetAmounts: [],
  selectedPreset: null,
  onSelectPreset: noop,
  topupAmount: 5000000,
  onTopupAmountChange: noop,
  paymentCurrency: 'USD',
  paymentAmount: 9.6,
  selectedPaymentMethod: method,
  calculating: false,
  onPaymentMethodSelect: noop,
  paymentLoading: null,
  redemptionCode: '',
  onRedemptionCodeChange: noop,
  onRedeem: noop,
  redeeming: false,
}

let root: ReturnType<typeof createRoot> | undefined
let container: HTMLDivElement
async function render(overrides: Partial<typeof defaults> = {}) {
  if (!root) {
    container = document.createElement('div')
    document.body.append(container)
    root = createRoot(container)
  }
  await act(async () => {
    root?.render(
      <I18nextProvider i18n={i18n}>
        <PaymentCurrencyContext
          value={{ preference: 'USD', setPreference: noop }}
        >
          <RechargeFormCard {...defaults} {...overrides} />
        </PaymentCurrencyContext>
      </I18nextProvider>
    )
  })
  const summary = container.querySelector(
    '[data-slot="wallet-payment-summary"]'
  )
  assert.ok(summary)
  return summary
}

afterEach(async () => {
  await act(async () => root?.unmount())
  root = undefined
  container?.remove()
})
after(() => {
  useSystemConfigStore.setState({ config: originalConfig })
  window.close()
})

test('the selected top-up and actual payment remain distinct after an amount change', async () => {
  let summary = await render()
  assert.match(
    summary.querySelector('[data-slot="wallet-selected-topup"]')?.textContent ?? '',
    /10/
  )
  assert.match(summary.textContent ?? '', /9\.60/)
  summary = await render({ topupAmount: 10000000, paymentAmount: 19.2 })
  assert.match(
    summary.querySelector('[data-slot="wallet-selected-topup"]')?.textContent ?? '',
    /20/
  )
  assert.match(summary.textContent ?? '', /19\.20/)
})

test('Edit returns focus to the amount input without starting a payment', async () => {
  let payments = 0
  const summary = await render({ onProceedToPayment: () => payments++ })
  const input = container.querySelector<HTMLInputElement>('#topup-amount')
  const edit = summary.querySelector<HTMLButtonElement>(
    '[aria-controls="topup-amount"]'
  )
  assert.ok(input)
  assert.ok(edit)
  let scrolled = false
  input.scrollIntoView = (options) => {
    assert.equal(typeof options === 'object' && options.block, 'center')
    scrolled = true
  }
  await act(async () => edit.click())
  assert.equal(scrolled, true)
  assert.equal(document.activeElement, input)
  assert.equal(payments, 0)
})

test('a pending or missing quote keeps payment disabled', async () => {
  for (const overrides of [
    { calculating: true },
    { paymentAmount: 0, quoteError: 'Quote unavailable' },
    { paymentLoading: method.type },
  ]) {
    const summary = await render(overrides)
    const pay = summary.querySelector<HTMLButtonElement>(
      'button:not([aria-controls])'
    )
    assert.ok(pay)
    assert.equal(pay.disabled, true)
  }
})
