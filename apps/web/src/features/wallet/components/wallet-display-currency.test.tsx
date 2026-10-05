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
const { PaymentConfirmDialog } =
  await import('./dialogs/payment-confirm-dialog')
const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: { translation: {} },
    zh: { translation: {} },
    'zh-TW': { translation: {} },
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

function Harness() {
  const [quota, setQuota] = useState(50000000)
  const [confirm, setConfirm] = useState(false)
  return (
    <>
      <WalletStatsCard
        user={{
          id: 7,
          username: 'unit-fixture',
          quota: 3359744,
          used_quota: 1679872,
          request_count: 1,
          aff_quota: 0,
          aff_history_quota: 0,
          aff_count: 0,
          group: 'default',
        }}
      />
      <RechargeFormCard
        topupInfo={{
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
        }}
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
        paymentAmount={100}
        paymentCurrency='CNY'
        selectedPaymentMethod={method}
        calculating={false}
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
        paymentAmount={100}
        paymentCurrency='CNY'
        paymentMethod={method}
        calculating={false}
        processing={false}
      />
    </>
  )
}

async function render() {
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
            <Harness />
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
  setRates(3359744, 6.719488)
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
  assert.ok(container.textContent?.includes('6.72 CNY'))
  assert.ok(container.textContent?.includes('3.36 CNY'))
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
  assert.ok(container.textContent?.includes('3,359,744 Credits'))
  assert.ok(container.textContent?.includes('1,679,872 Credits'))
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
  assert.equal(input(container).value, '14.882086254190795489180128009754')
  assert.ok(container.textContent?.includes('3,359,744 Credits'))
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

for (const language of ['en', 'zh', 'zh-TW']) {
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
    assert.equal(
      input(container).value,
      language === 'en' ? '14.882086254190795489180128009754' : '100'
    )
    assert.equal(
      input(container)
        .closest('[data-slot="input-group"]')
        ?.textContent?.includes('Credits'),
      false
    )
    assert.equal(updates.length, 0)
  })
}

test('fiat recharge reads live server K and FX while unit switches preserve raw selected quota', async () => {
  const container = await render()
  await choose(container, 'Recharge display currency', 'CNY')
  await act(async () => setRates(4000000, 6.5))
  assert.equal(input(container).value, '81.25')
  await choose(container, 'Recharge display currency', 'USD')
  assert.equal(input(container).value, '12.5')
  const setter = Object.getOwnPropertyDescriptor(
    domWindow.HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(setter)
  await act(async () => {
    setter.call(input(container), '25')
    input(container).dispatchEvent(new Event('input', { bubbles: true }))
  })
  assert.deepEqual(edits, [100000000])
  await choose(container, 'Recharge display currency', 'CNY')
  assert.equal(input(container).value, '162.5')
  assert.deepEqual(edits, [100000000])
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

test('public credit face value changes balance display while 100 CNY recharge retains the old ledger basis', async () => {
  const { mapStatusDataToConfig } = await import('@/hooks/use-system-config')
  const status = {
    currency_unit: 'credit',
    credits_per_usd: 3359744,
    ledger_quota_per_usd: 3359744,
    ledger_quota_per_usd_exact: '3359744',
    public_credits_per_usd: 100000,
    public_credits_per_usd_exact: '100000',
    credit_unit_schema_version: 2,
    quota_unit: 'LEDGER_QUOTA',
    public_credit_unit: 'CREDIT',
    legacy_credit_unit: 'LEDGER_QUOTA',
    cny_per_usd: '6.719488',
    quota_per_unit: 500000,
  }
  useSystemConfigStore.getState().setConfig(mapStatusDataToConfig(status))
  const container = await render()
  assert.ok(container.textContent?.includes('1 USD'))
  assert.ok(container.textContent?.includes('0.5 USD'))
  await choose(container, 'Recharge display currency', 'CNY')
  assert.equal(input(container).value, '100')
  await choose(container, 'Balance display currency', 'Credits')
  assert.ok(container.textContent?.includes('100,000 Credits'))
  assert.ok(container.textContent?.includes('50,000 Credits'))
  assert.equal(input(container).value, '100')
  assert.ok(container.textContent?.includes('100 CNY'))
  await act(async () =>
    useSystemConfigStore.getState().setConfig(
      mapStatusDataToConfig({
        ...status,
        public_credits_per_usd: 200000,
        public_credits_per_usd_exact: '200000',
      })
    )
  )
  assert.ok(container.textContent?.includes('200,000 Credits'))
  assert.ok(container.textContent?.includes('100,000 Credits'))
  assert.equal(input(container).value, '100')
  await choose(container, 'Balance display currency', 'USD')
  assert.ok(container.textContent?.includes('1 USD'))
  assert.ok(container.textContent?.includes('0.5 USD'))
  assert.equal(input(container).value, '100')
  assert.equal(
    useSystemConfigStore.getState().config.currency.creditsPerUsd,
    3359744
  )
  const preset = container.querySelector<HTMLButtonElement>(
    'button[aria-pressed]'
  )
  assert.ok(preset)
  await act(async () => preset.click())
  assert.deepEqual(selections, [50000000])
  assert.equal(edits.length, 0)
})
