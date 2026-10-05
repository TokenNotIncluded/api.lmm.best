/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({ url: 'https://console.example.test/wallet' })
domWindow.document.write(
  '<!doctype html><html><head></head><body></body></html>'
)
Object.defineProperty(domWindow.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
const globals = new Map<string, PropertyDescriptor | undefined>()
for (const key of [
  'window',
  'document',
  'navigator',
  'localStorage',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'PointerEvent',
  'FocusEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'matchMedia',
  'customElements',
  'CSSStyleSheet',
] as const) {
  globals.set(key, Object.getOwnPropertyDescriptor(globalThis, key))
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { useSystemConfigStore } = await import('@/stores/system-config-store')
const { useWalletCurrencyPreferenceStore } =
  await import('@/stores/wallet-currency-preference-store')
const { PaymentCurrencyProvider } =
  await import('../hooks/payment-currency-provider')
const { WalletStatsCard } = await import('./wallet-stats-card')
const { RechargeFormCard } = await import('./recharge-form-card')
const { parsePaymentDiscount } = await import('../lib/payment-discount')
const { walletCatalog } = await import('../lib/wallet-fixtures.test-support')
const { PaymentConfirmDialog } =
  await import('./dialogs/payment-confirm-dialog')
const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: { translation: {} },
    zh: { translation: {} },
    'zh-TW': { translation: {} },
    zhCN: { translation: {} },
    zhTW: { translation: {} },
  },
})
const originalConfig = useSystemConfigStore.getState().config
const originalAuth = useAuthStore.getState().auth
const originalPreference =
  useWalletCurrencyPreferenceStore.getState().preference
const originalPut = api.put
const reactGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
const originalAct = reactGlobals.IS_REACT_ACT_ENVIRONMENT
reactGlobals.IS_REACT_ACT_ENVIRONMENT = true
const mounted: Array<{
  root: ReturnType<typeof createRoot>
  container: HTMLDivElement
  client: InstanceType<typeof QueryClient>
}> = []
const updates: Array<{ path: string; body: unknown }> = []
const selections: number[] = []
const edits: number[] = []
const method = {
  name: 'Alipay',
  type: 'alipay',
  min_topup_credit: '1',
  settlement_currency: 'CNY',
  unit_price: '1',
}

function Harness({
  amount = 100,
  paymentDiscount = null,
  calculating = false,
  couponPercent,
  couponCode,
  couponApplied = false,
  validatedCouponCode = '',
  rawQuota = 50000000,
}: {
  amount?: number
  paymentDiscount?: ReturnType<typeof parsePaymentDiscount>
  calculating?: boolean
  couponPercent?: number
  couponCode?: string
  couponApplied?: boolean
  validatedCouponCode?: string
  rawQuota?: number
} = {}) {
  const [quota, setQuota] = useState(rawQuota)
  const [confirm, setConfirm] = useState(false)
  return (
    <>
      <WalletStatsCard
        user={{
          id: 7,
          username: 'unit-fixture',
          quota: 500000,
          used_quota: 250000,
          request_count: 1,
          aff_quota: 0,
          aff_history_quota: 0,
          aff_count: 0,
          group: 'default',
        }}
      />
      <RechargeFormCard
        topupInfo={walletCatalog(
          {
            enable_online_topup: true,
            enable_stripe_topup: false,
            stripe_min_topup: 1,
            amount_options: [],
            discount: {},
            min_topup: 1,
            pay_methods: [method],
            credit_metadata_version: 1,
            credit_min_topup: 1,
            credit_amount_options: [50000000],
            credit_discount: {},
          },
          500000
        )}
        presetAmounts={[{ value: 50000000, discount: 1 }]}
        selectedPreset={50000000}
        onSelectPreset={(preset) => {
          selections.push(preset.value)
          setQuota(preset.value)
        }}
        topupAmount={quota}
        onTopupAmountChange={(value) => {
          edits.push(value)
          setQuota(value)
        }}
        paymentAmount={amount}
        paymentDiscount={paymentDiscount}
        paymentCurrency='CNY'
        discountCode={
          couponCode ?? (couponPercent === undefined ? '' : 'SAVE10')
        }
        discountApplied={couponApplied}
        appliedDiscountCode={validatedCouponCode}
        discountPercent={couponPercent}
        selectedPaymentMethod={method}
        calculating={calculating}
        onPaymentMethodSelect={() => setConfirm(true)}
        paymentLoading={null}
        redemptionCode=''
        onRedemptionCodeChange={() => undefined}
        onRedeem={() => undefined}
        redeeming={false}
      />
      <PaymentConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        onConfirm={() => undefined}
        topupAmount={quota}
        creditedQuota={50000000}
        paymentAmount={amount}
        paymentDiscount={paymentDiscount}
        paymentCurrency='CNY'
        discountCode={couponPercent === undefined ? '' : 'SAVE10'}
        discountPercent={couponPercent}
        paymentMethod={method}
        calculating={calculating}
        processing={false}
      />
    </>
  )
}

async function render(props: Parameters<typeof Harness>[0] = {}) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  mounted.push({ root, container, client })
  await act(async () =>
    root.render(
      <QueryClientProvider client={client}>
        <I18nextProvider i18n={i18n}>
          <PaymentCurrencyProvider>
            <Harness {...props} />
          </PaymentCurrencyProvider>
        </I18nextProvider>
      </QueryClientProvider>
    )
  )
  return container
}
async function choose(container: HTMLElement, label: string, value: string) {
  const trigger = container.querySelector<HTMLButtonElement>(
    `button[aria-label="${label}"]`
  )
  assert.ok(trigger, label)
  await act(async () => trigger.click())
  const popup = document.querySelector<HTMLElement>(
    '[data-slot="select-content"][data-open]'
  )
  assert.ok(popup, 'active currency selector')
  const options = [...popup.querySelectorAll<HTMLElement>('[role="option"]')]
  if (label === 'Recharge display currency') {
    assert.deepEqual(
      options.map((option) => option.textContent?.trim()),
      ['CNY', 'USD']
    )
  }
  const option = options.find((entry) => entry.textContent?.trim() === value)
  assert.ok(option, `missing option ${value}`)
  await act(async () => {
    option.click()
    await new Promise((resolve) => setTimeout(resolve, 30))
  })
}
function input(container: HTMLElement) {
  const field = container.querySelector<HTMLInputElement>('#topup-amount')
  assert.ok(field)
  return field
}
function setRates(creditsPerUsd: number, cnyPerUsd: number) {
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...originalConfig.currency,
      currencyUnit: 'credit',
      quotaPerUnit: 500000,
      creditsPerUsd,
      creditsPerUsdExact: String(creditsPerUsd),
      cnyPerUsd,
      cnyPerUsdExact: String(cnyPerUsd),
    },
  })
}
beforeEach(async () => {
  updates.length = 0
  selections.length = 0
  edits.length = 0
  useAuthStore.getState().auth.setUser({
    id: 7,
    username: 'unit-fixture',
    role: 1,
    setting: { wallet_display_currency: '', settlement_currency: 'CNY' },
  })
  useWalletCurrencyPreferenceStore.getState().setPreference('')
  setRates(500000, 1)
  api.put = (async (path: string, body: unknown) => {
    updates.push({ path, body })
    return { data: { success: true } }
  }) as typeof api.put
  await i18n.changeLanguage('en')
})
afterEach(async () => {
  for (const { root, container, client } of mounted.splice(0)) {
    await act(async () => root.unmount())
    client.clear()
    container.remove()
  }
  api.put = originalPut
  useSystemConfigStore.getState().setConfig(originalConfig)
  useAuthStore.setState({ auth: originalAuth })
  useWalletCurrencyPreferenceStore.getState().setPreference(originalPreference)
})
after(() => {
  reactGlobals.IS_REACT_ACT_ENVIRONMENT = originalAct
  domWindow.close()
  for (const [key, descriptor] of globals) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor)
    else Reflect.deleteProperty(globalThis, key)
  }
})

test('balance CNY/USD/Credit selector updates balance and total usage without changing 100 CNY recharge or quote', async () => {
  await i18n.changeLanguage('zh')
  const container = await render()
  assert.equal(input(container).value, '100')
  assert.ok(container.textContent?.includes('1 CNY'))
  assert.ok(container.textContent?.includes('0.5 CNY'))
  for (const unit of ['Credits', 'USD', 'CNY', 'Credits']) {
    await choose(container, 'Balance display currency', unit)
    assert.equal(input(container).value, '100')
    assert.equal(
      container
        .querySelector('[aria-label="Recharge display currency"]')
        ?.textContent?.trim(),
      'CNY'
    )
    assert.ok(container.textContent?.includes('100 CNY'))
    assert.equal(
      container
        .querySelector('button[aria-pressed]')
        ?.textContent?.includes('Credits'),
      false
    )
  }
  assert.ok(container.textContent?.includes('500,000 Credits'))
  assert.ok(container.textContent?.includes('250,000 Credits'))
  assert.equal(edits.length, 0)
  assert.deepEqual(
    updates.map((update) => update.body),
    [
      { wallet_display_currency: 'CREDIT' },
      { wallet_display_currency: 'USD' },
      { wallet_display_currency: 'CNY' },
      { wallet_display_currency: 'CREDIT' },
    ]
  )
  assert.ok(updates.every((update) => update.path === '/api/user/self'))
  await choose(container, 'Recharge display currency', 'USD')
  assert.equal(input(container).value, '100')
  assert.ok(container.textContent?.includes('500,000 Credits'))
  assert.equal(edits.length, 0)
  assert.equal(updates.length, 4)
  const preset = container.querySelector<HTMLButtonElement>(
    'button[aria-pressed]'
  )
  assert.ok(preset)
  await act(async () => preset.click())
  assert.deepEqual(selections, [50000000])
  const pay = container.querySelector<HTMLButtonElement>(
    'button[aria-label="Alipay"]'
  )
  assert.ok(pay)
  await act(async () => pay.click())
  const dialog = document.querySelector('[role="alertdialog"]')
  assert.ok(dialog?.textContent?.includes('100 CNY'))
  assert.equal(dialog?.textContent?.includes('Credits'), false)
})

for (const language of ['en', 'zh', 'zh-TW', 'zhCN', 'zhTW']) {
  test(`recharge defaults to ${language === 'en' ? 'USD' : 'CNY'} by language even with saved balance Credit preference (${language})`, async () => {
    const auth = useAuthStore.getState().auth
    assert.ok(auth.user)
    auth.setUser({
      ...auth.user,
      setting: { wallet_display_currency: 'CREDIT' },
    })
    await i18n.changeLanguage(language)
    const container = await render()
    const unit = language === 'en' ? 'USD' : 'CNY'
    assert.equal(
      container
        .querySelector('[aria-label="Recharge display currency"]')
        ?.textContent?.trim(),
      unit
    )
    assert.equal(input(container).value, language === 'en' ? '100' : '100')
    assert.equal(
      input(container)
        .closest('[data-slot="input-group"]')
        ?.textContent?.includes('Credits'),
      false
    )
    assert.equal(updates.length, 0)
  })
}

test('fiat recharge uses fixed Credits per USD and live FX while preserving raw quota', async () => {
  const container = await render()
  await choose(container, 'Recharge display currency', 'CNY')
  await act(async () => setRates(500000, 6.5))
  assert.equal(input(container).value, '650')
  await choose(container, 'Recharge display currency', 'USD')
  assert.equal(input(container).value, '100')
  const setter = Object.getOwnPropertyDescriptor(
    domWindow.HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(setter)
  await act(async () => {
    setter.call(input(container), '25')
    input(container).dispatchEvent(new Event('input', { bubbles: true }))
  })
  assert.deepEqual(edits, [12500000])
  await choose(container, 'Recharge display currency', 'CNY')
  assert.equal(input(container).value, '162.5')
  assert.deepEqual(edits, [12500000])
  assert.equal(updates.length, 0)
})

test('unknown server conversion rates cannot invent a fiat recharge value', async () => {
  const container = await render()
  await act(async () => setRates(Number.NaN, Number.NaN))
  assert.equal(input(container).value, '')
  assert.equal(input(container).disabled, true)
  assert.equal(edits.length, 0)
})

test('failed balance preference save keeps the prior unit and cannot alter a fiat recharge', async () => {
  await i18n.changeLanguage('zh')
  let finish: ((value: { data: { success: boolean } }) => void) | undefined
  api.put = (() =>
    new Promise<{ data: { success: boolean } }>((resolve) => {
      finish = resolve
    })) as typeof api.put
  const container = await render()
  await choose(container, 'Balance display currency', 'Credits')
  const selector = container.querySelector<HTMLButtonElement>(
    '[aria-label="Balance display currency"]'
  )
  assert.ok(selector?.disabled)
  assert.equal(input(container).value, '100')
  assert.ok(finish)
  await act(async () => finish?.({ data: { success: false } }))
  assert.equal(selector.disabled, false)
  assert.equal(selector.textContent?.trim(), 'CNY')
  assert.ok(
    container
      .querySelector('[role="alert"]')
      ?.textContent?.includes(
        'Could not save balance display currency. Try again.'
      )
  )
  assert.equal(input(container).value, '100')
  assert.equal(edits.length, 0)
  assert.equal(selections.length, 0)
})

test('old ledger basis disables fiat recharge and old public face values cannot revalue Credits', async () => {
  const { mapStatusDataToConfig } = await import('@/hooks/use-system-config')
  for (const basis of [
    { ledger: 3359744, public: 100000 },
    { ledger: 500000, public: 100000 },
  ]) {
    await act(async () =>
      useSystemConfigStore.getState().setConfig(
        mapStatusDataToConfig({
          currency_unit: 'credit',
          credits_per_usd: basis.ledger,
          ledger_quota_per_usd: basis.ledger,
          ledger_quota_per_usd_exact: String(basis.ledger),
          public_credits_per_usd: basis.public,
          public_credits_per_usd_exact: String(basis.public),
          credit_unit_schema_version: 2,
          quota_unit: 'LEDGER_QUOTA',
          public_credit_unit: 'CREDIT',
          legacy_credit_unit: 'LEDGER_QUOTA',
          cny_per_usd: '6.719488',
          quota_per_unit: 500000,
        })
      )
    )
    const container = await render()
    assert.equal(input(container).value, basis.ledger === 500000 ? '100' : '')
    assert.equal(input(container).disabled, basis.ledger !== 500000)
    await choose(container, 'Balance display currency', 'Credits')
    assert.equal(container.textContent?.includes('100,000 Credits'), false)
    assert.equal(container.textContent?.includes('500,000 Credits'), false)
    assert.deepEqual(edits, [])
  }
})

test('short recharge display restores the exact draft on focus and never changes the selected ledger amount on blur', async () => {
  const container = await render()
  const field = input(container)
  assert.equal(field.value, '100')
  await act(async () => field.focus())
  assert.equal(field.value, '100')
  await act(async () => field.blur())
  assert.equal(field.value, '100')
  assert.deepEqual(edits, [])
  const micro = await render({ rawQuota: 1 })
  const microField = input(micro)
  assert.equal(microField.value, '0.000002')
  await act(async () => microField.focus())
  assert.equal(microField.value, '0.000002')
  await act(async () => microField.blur())
  assert.equal(microField.value, '0.000002')
  assert.deepEqual(edits, [])
})

for (const quote of [
  {
    original: '100.00',
    paid: '90.00',
    savings: '10.00',
    percent: '10.00',
    displayedPercent: '10',
    code: false,
  },
  {
    original: '100.00',
    paid: '72.00',
    savings: '28.00',
    percent: '28.00',
    displayedPercent: '28',
    code: true,
  },
  {
    original: '2.00',
    paid: '1.49',
    savings: '0.51',
    percent: '25.50',
    displayedPercent: '25.5',
    code: true,
  },
]) {
  test(`recharge and confirmation show the same authoritative CNY original, ${quote.displayedPercent}% discount, paid amount and savings`, async () => {
    await i18n.changeLanguage('zh')
    const discount = parsePaymentDiscount(
      {
        schema_version: 1,
        currency: 'CNY',
        basis: 'amount_preset_and_code',
        original_amount: quote.original,
        paid_amount: quote.paid,
        savings_amount: quote.savings,
        discount_percent: quote.percent,
      },
      quote.paid,
      'CNY'
    )
    assert.ok(discount)
    const container = await render({
      amount: Number(quote.paid),
      paymentDiscount: discount,
      couponPercent: quote.code ? 10 : undefined,
    })
    assert.ok(
      container.textContent?.includes(
        `Discount applied: ${quote.displayedPercent}% off`
      )
    )
    assert.ok(
      container.textContent?.includes(`You save: ${Number(quote.savings)} CNY`)
    )
    const original = [...container.querySelectorAll('.line-through')]
    assert.ok(
      original.length >= 2,
      'preset and checkout both show the real original'
    )
    assert.ok(
      original.every(
        (element) => element.textContent === `${Number(quote.original)} CNY`
      )
    )
    await choose(container, 'Balance display currency', 'Credits')
    assert.equal(input(container).value, '100')
    assert.ok(container.textContent?.includes(`${Number(quote.paid)} CNY`))
    const pay = container.querySelector<HTMLButtonElement>(
      'button[aria-label="Alipay"]'
    )
    assert.ok(pay)
    await act(async () => pay.click())
    const dialog = document.querySelector('[role="alertdialog"]')
    assert.ok(
      dialog?.textContent?.includes(
        `Discount applied: ${quote.displayedPercent}% off`
      )
    )
    assert.ok(
      dialog?.textContent?.includes(`You save: ${Number(quote.savings)} CNY`)
    )
    assert.equal(
      dialog?.querySelector('.line-through')?.textContent,
      `${Number(quote.original)} CNY`
    )
    assert.equal(dialog?.textContent?.includes('Credits'), false)
    assert.deepEqual(edits, [])
  })
}

test('preset discounts do not claim that an empty or unvalidated coupon was applied', async () => {
  const discount = parsePaymentDiscount(
    {
      schema_version: 1,
      currency: 'CNY',
      basis: 'amount_preset_and_code',
      original_amount: '100.00',
      paid_amount: '90.00',
      savings_amount: '10.00',
      discount_percent: '10.00',
    },
    '90.00',
    'CNY'
  )
  assert.ok(discount)
  for (const props of [
    {},
    { couponCode: 'PREVIEW20' },
    { couponCode: 'PREVIEW20', couponApplied: true },
    {
      couponCode: 'OTHER',
      couponApplied: true,
      validatedCouponCode: 'PREVIEW20',
    },
  ]) {
    const container = await render({
      amount: 90,
      paymentDiscount: discount,
      ...props,
    })
    const zone = container
      .querySelector('#discount-code')
      ?.closest('[data-slot="field"]')
    assert.ok(zone)
    assert.equal(zone.textContent?.includes('% off'), false)
    assert.equal(zone.textContent?.includes('You save:'), false)
    assert.equal(zone.querySelector('[data-slot="badge"]'), null)
    assert.ok(container.textContent?.includes('Discount applied: 10% off'))
    assert.ok(container.textContent?.includes('You save: 10 CNY'))
  }
})

test('a verified applied coupon shows the same total 28% discount in its zone and checkout', async () => {
  const discount = parsePaymentDiscount(
    {
      schema_version: 1,
      currency: 'CNY',
      basis: 'amount_preset_and_code',
      original_amount: '100.00',
      paid_amount: '72.00',
      savings_amount: '28.00',
      discount_percent: '28.00',
    },
    '72.00',
    'CNY'
  )
  assert.ok(discount)
  for (const props of [
    {},
    { calculating: true },
    { amount: 90 },
    { paymentDiscount: { ...discount, currency: 'USD' } },
  ]) {
    const container = await render({
      amount: 72,
      paymentDiscount: discount,
      couponCode: 'PREVIEW20',
      couponPercent: 20,
      couponApplied: true,
      validatedCouponCode: 'PREVIEW20',
      ...props,
    })
    const zone = container
      .querySelector('#discount-code')
      ?.closest('[data-slot="field"]')
    assert.ok(zone)
    const currentQuote = Object.keys(props).length === 0
    assert.equal(
      zone.textContent?.includes('Discount applied: 28% off'),
      currentQuote
    )
    assert.equal(zone.textContent?.includes('You save: 28 CNY'), currentQuote)
    assert.equal(zone.textContent?.includes('20% off'), false)
    if (currentQuote) {
      const pay = container.querySelector<HTMLButtonElement>(
        'button[aria-label="Alipay"]'
      )
      assert.ok(pay)
      await act(async () => pay.click())
      const dialog = document.querySelector('[role="alertdialog"]')
      assert.ok(dialog?.textContent?.includes('Discount applied: 28% off'))
      assert.ok(dialog?.textContent?.includes('You save: 28 CNY'))
    }
  }
})

test('missing, mismatched and pending discount metadata never shows a promotional original or percent', async () => {
  const discount = parsePaymentDiscount(
    {
      schema_version: 1,
      currency: 'CNY',
      basis: 'amount_preset_and_code',
      original_amount: '100.00',
      paid_amount: '90.00',
      savings_amount: '10.00',
      discount_percent: '10.00',
    },
    '90.00',
    'CNY'
  )
  assert.ok(discount)
  for (const props of [
    { amount: 90, couponPercent: 10 },
    { amount: 80, paymentDiscount: discount, couponPercent: 10 },
    { amount: 90, paymentDiscount: { ...discount, currency: 'USD' } },
    {
      amount: 90,
      paymentDiscount: discount,
      calculating: true,
      couponPercent: 10,
    },
  ]) {
    const container = await render(props)
    assert.equal(container.querySelector('.line-through'), null)
    assert.equal(container.textContent?.includes('% off'), false)
    assert.equal(container.textContent?.includes('You save:'), false)
  }
})
