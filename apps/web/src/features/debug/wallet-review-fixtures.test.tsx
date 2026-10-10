/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window({ url: 'http://127.0.0.1:4174/wallet?console_review=1' })
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'localStorage',
  'getComputedStyle',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { CreditAmountInput } = await import('@/components/credit-amount-input')
const { useTopupInfo } = await import('@/features/wallet/hooks/use-topup-info')
const { calculateCreditAmount } = await import('@/features/wallet/api')
const { useWalletCurrency } = await import('@/hooks/use-wallet-currency')
const { mapStatusDataToConfig } = await import('@/hooks/use-system-config')
const { api } = await import('@/lib/http-client')
const { useAuthStore } = await import('@/stores/auth-store')
const { useSystemConfigStore } = await import('@/stores/system-config-store')
const { withConsolePageFixtures } = await import('./console-page-fixtures')
const { withConsoleQueryFixtures } = await import('./console-query-fixtures')
const { DEBUG_CURRENCY_STATUS } = await import('./wallet-review-fixtures')
const originalAdapter = api.defaults.adapter
const originalConfig = useSystemConfigStore.getState().config
const originalAuth = useAuthStore.getState().auth
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
after(() => {
  api.defaults.adapter = originalAdapter
  useSystemConfigStore.setState({ config: originalConfig })
  useAuthStore.setState({ auth: originalAuth })
  dom.close()
})

test('real wallet hooks accept the review raw catalog and keep quote ISO independent of display selection', async () => {
  api.defaults.adapter = withConsoleQueryFixtures(
    withConsolePageFixtures(async () => {
      throw new Error('unmocked preview request')
    })
  )
  useSystemConfigStore
    .getState()
    .setConfig(mapStatusDataToConfig(DEBUG_CURRENCY_STATUS))
  const user = { id: 1002, username: 'preview', role: 1 }
  useAuthStore
    .getState()
    .auth.setUser({ ...user, setting: { wallet_display_currency: 'USD' } })
  let latest: ReturnType<typeof useTopupInfo> | undefined
  let quota = 0
  function Harness() {
    latest = useTopupInfo()
    const display = useWalletCurrency()
    const [credits, setCredits] = useState(3500000)
    quota = credits
    return (
      <div>
        <label htmlFor='review-quota'>{display.label}</label>
        <CreditAmountInput
          id='review-quota'
          value={credits}
          onValueChange={setCredits}
        />
        <output>{display.formatQuota(credits)}</output>
      </div>
    )
  }
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  try {
    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <Harness />
        </I18nextProvider>
      )
      await new Promise((resolve) => setTimeout(resolve, 25))
    })
    assert.ok(latest)
    assert.equal(latest.loading, false)
    assert.equal(latest.error, null)
    assert.equal(latest.topupInfo?.enable_online_topup, true)
    assert.equal(latest.topupInfo?.amount_unit, 'CREDIT')
    assert.equal(latest.topupInfo?.min_topup, 500000)
    assert.equal(latest.topupInfo?.stripe_min_topup, 500000)
    assert.equal(latest.topupInfo?.pay_methods[0].min_topup_credit, '3500000')
    assert.equal(latest.topupInfo?.pay_methods[0].max_topup_credit, '350000000')
    assert.deepEqual(
      latest.presetAmounts.map((preset) => preset.value),
      [5000000, 25000000, 50000000, 100000000]
    )
    for (const example of [
      { currency: 'USD', input: '7', output: '7 USD' },
      { currency: 'CNY', input: '49', output: '49 CNY' },
      { currency: 'CREDIT', input: '3500000', output: '3,500,000 Credits' },
    ]) {
      await act(async () =>
        useAuthStore.getState().auth.setUser({
          ...user,
          setting: { wallet_display_currency: example.currency },
        })
      )
      assert.equal(
        container.querySelector<HTMLInputElement>('input')?.value,
        example.input
      )
      assert.equal(
        container.querySelector('output')?.textContent,
        example.output
      )
      assert.equal(quota, 3500000)
      const quote = await calculateCreditAmount({
        amount: quota,
        payment_method: 'alipay',
      })
      assert.equal(quote.data, '49.00')
      assert.equal(quote.settlement_currency, 'CNY')
      assert.equal(quote.credited_quota, 3500000)
      assert.equal(quote.amount_unit, 'LEDGER_QUOTA')
      assert.equal('url' in quote, false)
    }
  } finally {
    await act(async () => root.unmount())
    container.remove()
  }
})
