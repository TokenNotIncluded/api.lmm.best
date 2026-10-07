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
*/
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import zhLocale from '@/i18n/locales/zh.json'

const domWindow = new Window({ url: 'https://console.example.test/wallet' })
domWindow.document.write(
  '<!doctype html><html><head></head><body></body></html>'
)
Object.defineProperty(domWindow.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
const domGlobals = [
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
] as const

for (const key of domGlobals) {
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

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: { translation: {} },
    zh: {
      translation: {
        ...zhLocale.translation,
        'Top-up amount': '充值金额',
        'Credited balance': '到账额度',
        'Wallet balance': '钱包余额',
        'Your balance is stored in credits. Display currency does not change the amount charged at checkout.':
          '余额按 credit 保存，切换显示币种不影响结算时的实际支付金额。',
      },
    },
  },
})

const { useEffect, useState } = await import('react')
const { RechargeFormCard } = await import('./recharge-form-card')
const { Wallet } = await import('../index')
const { walletCatalog, creditGrant } =
  await import('../lib/wallet-fixtures.test-support')
const { parsePaymentDiscount } = await import('../lib/payment-discount')
const { useTopupInfo } = await import('../hooks/use-topup-info')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { useSystemConfigStore } = await import('@/stores/system-config-store')
const { useWalletCurrencyPreferenceStore } =
  await import('@/stores/wallet-currency-preference-store')
const { PaymentCurrencyProvider } =
  await import('../hooks/payment-currency-provider')
const { usePaymentCurrency } = await import('../hooks/use-payment-currency')
const { PaymentConfirmDialog } =
  await import('./dialogs/payment-confirm-dialog')
const { formatCreditBalance, formatPaymentAmount, mergePresetAmounts } =
  await import('../lib')

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const originalConfig = useSystemConfigStore.getState().config
const originalGet = api.get
const originalPost = api.post
// oxlint-disable-next-line no-console -- The test captures and restores the expected production error log.
const originalConsoleError = console.error

async function editInput(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(
    domWindow.HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(setter)
  await act(async () => {
    setter.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await flushEffects()
  })
}

async function flushEffects() {
  await new Promise((resolve) => setTimeout(resolve, 20))
}

let latestTopupState: ReturnType<typeof useTopupInfo> | null = null

function TopupInfoProbe() {
  const state = useTopupInfo()
  useEffect(() => {
    latestTopupState = state
  }, [state])
  return (
    <div>
      {state.loading
        ? 'loading'
        : state.error
          ? `error:${state.topupInfo ? 1 : 0}:${state.presetAmounts.length}`
          : `ready:${state.topupInfo ? 1 : 0}:${state.presetAmounts.length}`}
    </div>
  )
}

let paymentDisplayFixture: 'CNY' | 'USD' = 'USD'

// Synthetic fiat parity keeps the 50M raw-Credit UI fixture at 100 CNY.
// Non-parity FX is covered by the explicit bridges and amount-arrow scenarios.
function setCnyBillingAndPaymentDisplay() {
  paymentDisplayFixture = 'CNY'
  useWalletCurrencyPreferenceStore.getState().setPreference('CNY')
  useSystemConfigStore.setState((state) => ({
    config: {
      ...state.config,
      currency: {
        ...state.config.currency,
        quotaDisplayType: 'CNY',
        usdExchangeRate: 7,
        currencyUnit: 'credit',
        creditsPerUsd: 500000,
        creditsPerUsdExact: '500000',
        cnyPerUsd: 1,
        cnyPerUsdExact: '1',
      },
    },
  }))
}

function setUsdBillingCurrency() {
  paymentDisplayFixture = 'USD'
  useWalletCurrencyPreferenceStore.getState().setPreference('USD')
  useSystemConfigStore.setState((state) => ({
    config: {
      ...state.config,
      currency: {
        ...state.config.currency,
        quotaDisplayType: 'USD',
        currencyUnit: 'credit',
        creditsPerUsd: 500000,
        creditsPerUsdExact: '500000',
        cnyPerUsd: 7,
        cnyPerUsdExact: '7',
      },
    },
  }))
}

type Rendered = {
  container: HTMLDivElement
  root: ReturnType<typeof createRoot>
}

const mounted = new Set<Rendered>()

function PaymentDisplayFixture({ children }: { children: React.ReactNode }) {
  const { setPreference } = usePaymentCurrency()
  useEffect(() => {
    setPreference(paymentDisplayFixture)
  }, [setPreference])
  return children
}

async function render(node: React.ReactNode): Promise<Rendered> {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  await act(async () => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <PaymentCurrencyProvider>
          <PaymentDisplayFixture>{node}</PaymentDisplayFixture>
        </PaymentCurrencyProvider>
      </I18nextProvider>
    )
  })

  const rendered = { container, root }
  mounted.add(rendered)
  return rendered
}

async function unmount(rendered: Rendered) {
  await act(async () => rendered.root.unmount())
  rendered.container.remove()
  mounted.delete(rendered)
}

beforeEach(() => {
  setUsdBillingCurrency()
})

afterEach(async () => {
  for (const rendered of mounted) await unmount(rendered)
  api.get = originalGet
  api.post = originalPost
  // oxlint-disable-next-line no-console -- Restore the original logger after every test.
  console.error = originalConsoleError
  useAuthStore.getState().auth.reset('complete')
  latestTopupState = null
})

after(() => {
  useSystemConfigStore.setState((state) => ({
    ...state,
    config: originalConfig,
  }))
  domWindow.close()
})

const topupInfo = walletCatalog({
  credit_metadata_version: 1,
  stripe_credit_max_topup: 5000000000,
  waffo_credit_max_topup: null,
  pancake_credit_max_topup: null,
  credit_amount_options: [50000000],
  credit_discount: {},
  credit_min_topup: 5000000,
  stripe_credit_min_topup: 5000000,
  waffo_credit_min_topup: 0,
  pancake_credit_min_topup: 0,
  enable_online_topup: true,
  enable_stripe_topup: false,
  pay_methods: [
    {
      name: 'Alipay',
      type: 'alipay',
      min_topup_credit: '0',
      settlement_unit: 'CNY',
      unit_price: '5.4',
    },
  ],
  min_topup: 5000000,
  stripe_min_topup: 5000000,
  amount_options: [100],
  discount: {},
})

describe('wallet payment clarity', () => {
  test('clears stale top-up configuration and presets when a refresh fails', async () => {
    const consoleErrors: unknown[][] = []
    // oxlint-disable-next-line no-console -- Capture and assert the expected failure log.
    console.error = (...args: unknown[]) => consoleErrors.push(args)
    let calls = 0
    api.get = (async (url) => {
      if (url !== '/api/user/topup/info') {
        return { data: { success: true, data: [] } }
      }
      calls += 1
      if (calls === 1) {
        return {
          data: {
            success: true,
            data: {
              ...topupInfo,
              amount_options: [10],
            },
          },
        }
      }
      return { data: { success: false, message: 'offline' } }
    }) as typeof api.get

    const rendered = await render(<TopupInfoProbe />)
    await act(flushEffects)
    assert.equal(rendered.container.textContent, 'ready:1:1')

    await act(async () => {
      await latestTopupState?.refetch()
    })
    assert.equal(rendered.container.textContent, 'error:0:0')
    assert.equal(calls, 2)
    assert.deepEqual(consoleErrors, [
      ['Failed to fetch topup info:', 'offline'],
    ])
    await unmount(rendered)
  })

  test('renders Creem-only top-up without requesting a generic amount quote', async () => {
    const user = {
      id: 7,
      username: 'creem-user',
      role: 1,
      developer_access_granted: false,
    }
    useAuthStore.getState().auth.setUser(user)
    const posts: string[] = []
    api.get = (async (url) => {
      if (url === '/api/status') {
        return { data: { success: true, data: {} } }
      }
      if (url === '/api/user/self') {
        return { data: { success: true, data: user } }
      }
      if (url === '/api/user/topup/info') {
        return {
          data: {
            success: true,
            data: {
              ...topupInfo,
              enable_online_topup: false,
              pay_methods: [],
              amount_options: [],
              enable_creem_topup: true,
              creem_products: [
                {
                  name: 'Starter pack',
                  productId: 'starter',
                  price: 5,
                  quota: 10,
                  currency: 'USD',
                },
              ],
            },
          },
        }
      }
      return { data: { success: true, data: [] } }
    }) as typeof api.get
    api.post = (async (url) => {
      posts.push(url)
      return { data: { success: true, data: '1' } }
    }) as typeof api.post

    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    const rendered = await render(
      <QueryClientProvider client={queryClient}>
        <Wallet />
      </QueryClientProvider>
    )
    await act(flushEffects)

    assert.equal(
      rendered.container.textContent?.includes('Payment option 1'),
      true
    )
    assert.deepEqual(
      posts.filter((url) =>
        [
          '/api/user/amount',
          '/api/user/stripe/amount',
          '/api/user/waffo/amount',
          '/api/user/waffo-pancake/amount',
        ].includes(url)
      ),
      []
    )

    await unmount(rendered)
    queryClient.clear()
  })

  test('renders Chinese platform title, preset card, and input addon without a dollar symbol', async () => {
    await i18n.changeLanguage('zh')
    setCnyBillingAndPaymentDisplay()

    assert.equal(formatCreditBalance(6.8), '6.8 CNY')
    assert.equal(formatCreditBalance(Number.NaN), '-')
    assert.equal(formatCreditBalance(6.8).includes('$'), false)
    assert.equal(formatPaymentAmount(1, 'USD'), '1 USD')
    assert.equal(formatPaymentAmount(6.8, 'CNY'), '6.8 CNY')

    const rendered = await render(
      <RechargeFormCard
        topupInfo={topupInfo}
        presetAmounts={[{ value: 5000000 }]}
        selectedPreset={null}
        onSelectPreset={() => undefined}
        topupAmount={5000000}
        onTopupAmountChange={() => undefined}
        paymentAmount={54}
        calculating={false}
        onPaymentMethodSelect={() => undefined}
        paymentLoading={null}
        redemptionCode=''
        onRedemptionCodeChange={() => undefined}
        onRedeem={() => undefined}
        redeeming={false}
      />
    )

    const text = rendered.container.textContent ?? ''
    const presetCard = rendered.container.querySelector('button[aria-pressed]')
    const addons = [
      ...rendered.container.querySelectorAll('[data-slot="input-group-addon"]'),
    ]
    assert.equal(
      [...rendered.container.querySelectorAll('label')].some(
        (label) => label.textContent === '到账额度'
      ),
      true
    )
    assert.equal(
      rendered.container.querySelector('label[for="topup-amount"]')
        ?.textContent,
      '充值金额 (CNY)'
    )
    assert.equal(
      presetCard
        ?.querySelector('[data-slot="wallet-credit-value"]')
        ?.textContent?.trim(),
      '10 CNY'
    )
    assert.equal(presetCard?.textContent?.includes('$'), false)
    assert.deepEqual(
      addons.map((addon) => addon.textContent),
      ['CNY?']
    )
    assert.equal(
      addons.some((addon) => addon.textContent?.includes('$')),
      false
    )
    assert.equal(text.includes('$'), false)
    const creditHelp = rendered.container.querySelector<HTMLButtonElement>(
      'button[aria-label="钱包余额"]'
    )
    assert.ok(creditHelp)
    await act(async () => {
      creditHelp.click()
      await flushEffects()
    })
    assert.equal(
      document.body.textContent?.includes(
        '余额按 credit 保存，切换显示币种不影响结算时的实际支付金额。'
      ),
      true
    )
    await unmount(rendered)
  })

  test('long press spinner accelerates and stops on pointer release', async () => {
    await i18n.changeLanguage('en')
    setUsdBillingCurrency()
    const changes: number[] = []

    function Harness() {
      const [amount, setAmount] = useState(5000000)
      return (
        <RechargeFormCard
          topupInfo={topupInfo}
          presetAmounts={[{ value: 5000000 }]}
          selectedPreset={null}
          onSelectPreset={() => undefined}
          topupAmount={amount}
          onTopupAmountChange={(value) => {
            changes.push(value)
            setAmount(value)
          }}
          paymentAmount={10}
          calculating={false}
          onPaymentMethodSelect={() => undefined}
          paymentLoading={null}
          redemptionCode=''
          onRedemptionCodeChange={() => undefined}
          onRedeem={() => undefined}
          redeeming={false}
        />
      )
    }

    const rendered = await render(<Harness />)
    const increase = rendered.container.querySelector(
      'button[aria-label="Increase amount"]'
    )
    assert.ok(increase)
    const down = new Event('pointerdown', { bubbles: true })
    Object.defineProperties(down, {
      button: { value: 0 },
      pointerId: { value: 500000 },
      pointerType: { value: 'mouse' },
    })
    await act(async () => increase.dispatchEvent(down))
    assert.deepEqual(changes, [5500000])
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 520))
    })
    const heldChanges = changes.length
    assert.ok(heldChanges >= 2, 'hold should produce repeated increments')

    await act(async () => {
      increase.dispatchEvent(new Event('pointerup', { bubbles: true }))
    })
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 240))
    })
    assert.equal(changes.length, heldChanges, 'release must stop the hold')
    await unmount(rendered)
  })

  test('Pancake presets distinguish unrequested quotes from the selected server quote', async () => {
    await i18n.changeLanguage('en')
    setUsdBillingCurrency()
    for (const calculating of [false, true]) {
      const rendered = await render(
        <RechargeFormCard
          topupInfo={topupInfo}
          presetAmounts={[{ value: 5000000 }, { value: 10000000 }]}
          selectedPreset={5000000}
          onSelectPreset={() => undefined}
          topupAmount={5000000}
          onTopupAmountChange={() => undefined}
          paymentAmount={999}
          settlementQuote={{ amount: '1.4900', currency: 'USD' }}
          selectedPaymentMethod={{
            name: 'Waffo Pancake',
            type: 'waffo_pancake',
            min_topup_credit: '0',
          }}
          calculating={calculating}
          onPaymentMethodSelect={() => undefined}
          paymentLoading={null}
          redemptionCode=''
          onRedemptionCodeChange={() => undefined}
          onRedeem={() => undefined}
          redeeming={false}
        />
      )
      const presets = Array.from(
        rendered.container.querySelectorAll('button[aria-pressed]')
      )
      assert.equal(presets.length, 2)
      const selectedLabel = presets[0].getAttribute('aria-label') || ''
      const unselectedLabel = presets[1].getAttribute('aria-label') || ''
      assert.equal(selectedLabel.includes('1.4900 USD'), !calculating)
      assert.equal(
        unselectedLabel.includes('Select to get the current payment quote'),
        true
      )
      assert.equal(unselectedLabel.includes('Payment unavailable'), false)
      assert.equal(unselectedLabel.includes('1.4900'), false)
      assert.equal(selectedLabel.includes('999'), false)
      await unmount(rendered)
    }
  })

  test('preset payment details appear only when they add information', async () => {
    await i18n.changeLanguage('en')
    setUsdBillingCurrency()
    for (const scenario of [
      { value: 745000, amount: '1.4900', currency: 'USD', show: false },
      { value: 745000, amount: '1.39', currency: 'USD', show: true },
      { value: 745000, amount: '1.49', currency: 'CNY', show: true },
      { value: 50000, amount: '0.101', currency: 'USD', show: true },
    ] as const) {
      const rendered = await render(
        <RechargeFormCard
          topupInfo={topupInfo}
          presetAmounts={[{ value: scenario.value }]}
          selectedPreset={scenario.value}
          onSelectPreset={() => undefined}
          topupAmount={scenario.value}
          onTopupAmountChange={() => undefined}
          paymentAmount={999}
          settlementQuote={{
            amount: scenario.amount,
            currency: scenario.currency,
          }}
          selectedPaymentMethod={{
            name: 'Waffo Pancake',
            type: 'waffo_pancake',
            min_topup_credit: '0',
          }}
          calculating={false}
          onPaymentMethodSelect={() => undefined}
          paymentLoading={null}
          redemptionCode=''
          onRedemptionCodeChange={() => undefined}
          onRedeem={() => undefined}
          redeeming={false}
        />
      )
      const preset = rendered.container.querySelector('button[aria-pressed]')
      assert.ok(preset)
      assert.equal(preset.textContent?.includes('Pay '), scenario.show)
      assert.equal(
        preset.getAttribute('aria-label')?.includes(scenario.amount),
        true,
        'the accessible name still identifies the authoritative quote'
      )
      assert.equal(preset.textContent?.includes('999'), false)
      await unmount(rendered)
    }
  })

  test('deduplicates identical preset credits without merging distinct rounded amounts', async () => {
    await i18n.changeLanguage('en')
    setUsdBillingCurrency()
    let selected = 0
    const rendered = await render(
      <RechargeFormCard
        topupInfo={topupInfo}
        presetAmounts={[
          { value: 5000000, discount: 0.9 },
          { value: 5000000, discount: 0.9 },
          { value: 5000001, discount: 0.9 },
        ]}
        selectedPreset={null}
        onSelectPreset={(preset) => {
          selected = preset.value
        }}
        topupAmount={5000000}
        onTopupAmountChange={() => undefined}
        paymentAmount={0}
        calculating={false}
        onPaymentMethodSelect={() => undefined}
        paymentLoading={null}
        redemptionCode=''
        onRedemptionCodeChange={() => undefined}
        onRedeem={() => undefined}
        redeeming={false}
      />
    )
    const presets = Array.from(
      rendered.container.querySelectorAll<HTMLButtonElement>(
        'button[aria-pressed]'
      )
    )
    assert.equal(presets.length, 2)
    assert.deepEqual(
      presets.map(
        (button) =>
          button.querySelector('[data-slot="wallet-credit-value"]')?.textContent
      ),
      ['10 USD', '10 USD']
    )
    assert.equal(
      presets.every((button) => button.textContent?.includes('10% off')),
      true
    )
    await act(async () => presets[1].click())
    assert.equal(selected, 5000001)
    await unmount(rendered)
  })

  test('failed quotes explain the server reason and allow a quote-only retry', async () => {
    await i18n.changeLanguage('en')
    let retries = 0
    const rendered = await render(
      <RechargeFormCard
        topupInfo={topupInfo}
        presetAmounts={[{ value: 5000000 }]}
        selectedPreset={5000000}
        onSelectPreset={() => undefined}
        topupAmount={5000000}
        onTopupAmountChange={() => undefined}
        paymentAmount={0}
        settlementQuote={null}
        selectedPaymentMethod={{ name: 'Waffo Pancake', type: 'waffo_pancake' }}
        calculating={false}
        quoteError='Minimum payment is 1 USD'
        onRetryQuote={() => {
          retries++
        }}
        onPaymentMethodSelect={() => {
          throw new Error('retry must not start checkout')
        }}
        paymentLoading={null}
        redemptionCode=''
        onRedemptionCodeChange={() => undefined}
        onRedeem={() => undefined}
        redeeming={false}
      />
    )
    const alert = rendered.container.querySelector('[role="alert"]')
    assert.ok(alert)
    assert.ok(alert.textContent?.includes('Minimum payment is 1 USD'))
    const retry = alert.querySelector('button')
    assert.ok(retry)
    await act(async () => retry.click())
    assert.equal(retries, 1)
    const payButton = Array.from(
      rendered.container.querySelectorAll('button')
    ).find((b) => b.textContent?.includes('Pay Payment unavailable'))
    assert.equal(payButton?.disabled, true)
    await unmount(rendered)
  })

  test('hides a stale payment quote while a fresh quote is calculating', async () => {
    await i18n.changeLanguage('en')
    setUsdBillingCurrency()
    const rendered = await render(
      <RechargeFormCard
        topupInfo={topupInfo}
        presetAmounts={[{ value: 5000000 }]}
        selectedPreset={5000000}
        onSelectPreset={() => undefined}
        topupAmount={5000000}
        onTopupAmountChange={() => undefined}
        paymentAmount={236.11}
        selectedPaymentMethod={{
          name: 'Waffo Pancake',
          type: 'waffo_pancake',
          min_topup_credit: '0',
          settlement_currency: 'USD',
          platform_units_per_usd: '6.8',
          settlement_units_per_usd: '1',
        }}
        calculating
        onPaymentMethodSelect={() => undefined}
        paymentLoading={null}
        redemptionCode=''
        onRedemptionCodeChange={() => undefined}
        onRedeem={() => undefined}
        redeeming={false}
      />
    )

    const text = rendered.container.textContent ?? ''
    assert.equal(text.includes('Calculating...'), true)
    assert.equal(text.includes('236.11 USD'), false)
    assert.equal(text.includes('Estimated payment:'), false)
    await unmount(rendered)
  })

  test('rejects zero and non-finite payment quotes in recharge and confirmation', async () => {
    await i18n.changeLanguage('en')
    setUsdBillingCurrency()

    for (const invalidAmount of [0, Number.NaN, Number.POSITIVE_INFINITY]) {
      const recharge = await render(
        <RechargeFormCard
          topupInfo={topupInfo}
          presetAmounts={[]}
          selectedPreset={null}
          onSelectPreset={() => undefined}
          topupAmount={5000000}
          onTopupAmountChange={() => undefined}
          paymentAmount={invalidAmount}
          calculating={false}
          onPaymentMethodSelect={() => undefined}
          paymentLoading={null}
          redemptionCode=''
          onRedemptionCodeChange={() => undefined}
          onRedeem={() => undefined}
          redeeming={false}
        />
      )

      const rechargeText = recharge.container.textContent ?? ''
      assert.equal(rechargeText.includes('Payment unavailable'), true)
      assert.equal(rechargeText.includes('0 USD'), false)
      assert.equal(rechargeText.includes('NaN'), false)
      assert.equal(rechargeText.includes('Infinity'), false)
      await unmount(recharge)

      const confirmation = await render(
        <PaymentConfirmDialog
          open
          onOpenChange={() => undefined}
          onConfirm={() => undefined}
          topupAmount={5000000}
          paymentAmount={invalidAmount}
          paymentMethod={{ name: 'Waffo Pancake', type: 'waffo_pancake' }}
          calculating={false}
          processing={false}
        />
      )
      const confirmButton = [...document.body.querySelectorAll('button')].find(
        (button) => button.textContent?.includes('Confirm Payment')
      )
      const confirmationText = document.body.textContent ?? ''
      assert.equal(confirmButton?.disabled, true)
      assert.equal(confirmationText.includes('Payment unavailable'), true)
      assert.equal(confirmationText.includes('Amount due: 0 USD'), false)
      assert.equal(confirmationText.includes('NaN'), false)
      assert.equal(confirmationText.includes('Infinity'), false)
      await unmount(confirmation)
    }
  })

  test('disables confirmation while calculating or processing and enables a positive quote', async () => {
    await i18n.changeLanguage('en')
    setUsdBillingCurrency()

    for (const state of [
      { calculating: true, processing: false, disabled: true },
      { calculating: false, processing: true, disabled: true },
      { calculating: false, processing: false, disabled: false },
    ]) {
      const confirmation = await render(
        <PaymentConfirmDialog
          open
          onOpenChange={() => undefined}
          onConfirm={() => undefined}
          topupAmount={500000}
          paymentAmount={0.15}
          paymentMethod={{ name: 'Alipay', type: 'alipay' }}
          calculating={state.calculating}
          processing={state.processing}
        />
      )
      const confirmButton = [...document.body.querySelectorAll('button')].find(
        (button) => button.textContent?.includes('Confirm Payment')
      )
      assert.equal(confirmButton?.disabled, state.disabled)
      assert.equal(
        document.body.textContent?.includes('0.15 USD'),
        !state.calculating
      )
      await unmount(confirmation)
    }
  })

  test('applies explicit USD bridge rates to preset, custom preview, and confirmation', async () => {
    await i18n.changeLanguage('en')
    setCnyBillingAndPaymentDisplay()
    const paymentMethod = {
      name: 'USD card',
      type: 'card',
      min_topup_credit: '3400000',
      settlement_currency: 'USD',
      platform_units_per_usd: '6.8',
      settlement_units_per_usd: '1',
      min_topup: 3400000,
      max_topup: '68',
    }
    const recharge = await render(
      <RechargeFormCard
        topupInfo={{
          ...topupInfo,
          min_topup: 3400000,
          pay_methods: [paymentMethod],
        }}
        presetAmounts={[{ value: 3400000 }]}
        selectedPreset={3400000}
        onSelectPreset={() => undefined}
        topupAmount={3400000}
        onTopupAmountChange={() => undefined}
        paymentAmount={1}
        selectedPaymentMethod={paymentMethod}
        calculating={false}
        onPaymentMethodSelect={() => undefined}
        paymentLoading={null}
        redemptionCode=''
        onRedemptionCodeChange={() => undefined}
        onRedeem={() => undefined}
        redeeming={false}
        priceRatio={99}
      />
    )

    const text = recharge.container.textContent ?? ''
    assert.ok(
      recharge.container.querySelector(
        '[aria-label="Preset amount: 6.8 CNY. Actual payment: 1 USD."]'
      )
    )
    assert.equal(
      recharge.container
        .querySelector('button[aria-pressed] [data-slot="wallet-credit-value"]')
        ?.textContent?.trim(),
      '6.8 CNY'
    )
    assert.equal(text.includes('Estimated payment: 1 USD'), false)
    assert.equal(text.includes('Amount due: 1 USD (actual payment)'), true)
    assert.equal(text.includes('$1'), false)
    assert.equal(
      recharge.container.querySelector('#topup-amount')?.getAttribute('min'),
      '6.8'
    )
    assert.equal(text.includes('1 USD / 6.8 CNY'), true)
    await unmount(recharge)

    const confirmation = await render(
      <PaymentConfirmDialog
        open
        onOpenChange={() => undefined}
        onConfirm={() => undefined}
        topupAmount={3400000}
        paymentAmount={1}
        paymentMethod={paymentMethod}
        calculating={false}
        processing={false}
        discountRate={1}
      />
    )
    assert.equal(
      document.body.textContent?.includes('Credit 6.8 CNY; pay 1 USD'),
      true
    )
    assert.equal(document.body.textContent?.includes('$1'), false)
    await unmount(confirmation)
  })

  test('applies explicit CNY bridge rates instead of the global display currency', async () => {
    await i18n.changeLanguage('en')
    setUsdBillingCurrency()
    const paymentMethod = {
      name: 'CNY gateway',
      type: 'card',
      min_topup_credit: '0',
      settlement_currency: 'CNY',
      platform_units_per_usd: '6.8',
      settlement_units_per_usd: '6.8',
    }
    const rendered = await render(
      <RechargeFormCard
        topupInfo={{ ...topupInfo, pay_methods: [paymentMethod] }}
        presetAmounts={[{ value: 3400000 }]}
        selectedPreset={3400000}
        onSelectPreset={() => undefined}
        topupAmount={3400000}
        onTopupAmountChange={() => undefined}
        paymentAmount={6.8}
        selectedPaymentMethod={paymentMethod}
        calculating={false}
        onPaymentMethodSelect={() => undefined}
        paymentLoading={null}
        redemptionCode=''
        onRedemptionCodeChange={() => undefined}
        onRedeem={() => undefined}
        redeeming={false}
        priceRatio={99}
      />
    )

    const text = rendered.container.textContent ?? ''
    assert.ok(
      rendered.container.querySelector(
        '[aria-label="Preset amount: 6.8 USD. Actual payment: 6.8 CNY."]'
      )
    )
    assert.equal(
      rendered.container
        .querySelector('button[aria-pressed] [data-slot="wallet-credit-value"]')
        ?.textContent?.trim(),
      '6.8 USD'
    )
    assert.equal(text.includes('Estimated payment: 6.8 CNY'), false)
    assert.equal(text.includes('Amount due: 6.8 CNY (actual payment)'), true)
    assert.equal(text.includes('6.8 CNY / 6.8 USD'), true)
    await unmount(rendered)
  })

  test('labels preset credits, payment, discount, and the custom-account destination', async () => {
    await i18n.changeLanguage('en')
    setCnyBillingAndPaymentDisplay()
    const rendered = await render(
      <RechargeFormCard
        topupInfo={topupInfo}
        presetAmounts={[
          { value: 50000000 },
          { value: 100000000, discount: 0.8 },
        ]}
        selectedPreset={null}
        onSelectPreset={() => undefined}
        topupAmount={50000000}
        onTopupAmountChange={() => undefined}
        paymentAmount={540}
        calculating={false}
        onPaymentMethodSelect={() => undefined}
        paymentLoading={null}
        redemptionCode=''
        onRedemptionCodeChange={() => undefined}
        onRedeem={() => undefined}
        redeeming={false}
        priceRatio={5.4}
      />
    )

    const noDiscountPreset = rendered.container.querySelector(
      '[aria-label="Preset amount: 100 CNY. Select to get the current payment quote."]'
    )
    assert.ok(noDiscountPreset)
    assert.equal(
      noDiscountPreset
        ?.querySelector('[data-slot="wallet-credit-value"]')
        ?.textContent?.includes('100'),
      true
    )
    assert.equal(
      noDiscountPreset?.textContent?.includes('Estimated actual payment'),
      false
    )

    const discountPreset = rendered.container.querySelector(
      '[aria-label="Preset amount: 200 CNY. Select to get the current payment quote. · 20% off"]'
    )
    assert.ok(discountPreset)
    assert.equal(discountPreset.getAttribute('aria-pressed'), 'false')
    assert.equal(discountPreset.textContent?.includes('20% off'), true)
    assert.equal(
      discountPreset?.textContent?.includes('Platform discount 20%'),
      false
    )
    assert.equal(
      discountPreset?.textContent?.includes('Estimated actual payment'),
      false
    )

    const customAmount = rendered.container.querySelector('#topup-amount')
    assert.ok(customAmount)
    assert.equal(
      rendered.container.querySelector('label[for="topup-amount"]')
        ?.textContent,
      'Top-up amount (CNY)'
    )
    assert.equal(
      rendered.container.querySelector('#topup-amount-description')
        ?.textContent,
      'Destination: current signed-in account · API usage balance'
    )
    assert.equal(
      customAmount?.getAttribute('aria-describedby'),
      'topup-amount-description'
    )
    assert.equal(
      rendered.container.textContent?.includes(
        'Selected method: Alipay · Amount due: 540 CNY (actual payment)'
      ),
      true
    )
    assert.equal(
      rendered.container.textContent?.includes('Platform discount 0%'),
      false
    )

    await unmount(rendered)
  })

  test('shows the 100 CNY recharge, 20%-discount payment breakdown', async () => {
    await i18n.changeLanguage('en')
    setCnyBillingAndPaymentDisplay()
    const rendered = await render(
      <RechargeFormCard
        topupInfo={{ ...topupInfo, discount: { 50000000: 0.8 } }}
        presetAmounts={[{ value: 50000000, discount: 0.8 }]}
        selectedPreset={50000000}
        onSelectPreset={() => undefined}
        topupAmount={50000000}
        onTopupAmountChange={() => undefined}
        paymentAmount={80}
        paymentCurrency='CNY'
        paymentDiscount={parsePaymentDiscount(
          {
            schema_version: 1,
            currency: 'CNY',
            basis: 'amount_preset_and_code',
            original_amount: '100.00',
            paid_amount: '80.00',
            savings_amount: '20.00',
            discount_percent: '20.00',
          },
          '80.00',
          'CNY'
        )}
        calculating={false}
        onPaymentMethodSelect={() => undefined}
        paymentLoading={null}
        redemptionCode=''
        onRedemptionCodeChange={() => undefined}
        onRedeem={() => undefined}
        redeeming={false}
        priceRatio={1}
      />
    )

    const text = rendered.container.textContent ?? ''
    assert.equal(
      rendered.container
        .querySelector('button[aria-pressed] [data-slot="wallet-credit-value"]')
        ?.textContent?.includes('100'),
      true
    )
    assert.equal(
      text.includes(
        'Selected method: Alipay · Amount due: 80 CNY (actual payment)'
      ),
      true
    )
    assert.equal(text.includes('Discount applied: 20% off'), true)
    assert.equal(text.includes('Discount applied 20 CNY'), true)

    assert.equal(
      rendered.container.querySelector('.line-through')?.textContent,
      '100 CNY'
    )
    assert.ok(text.includes('You save: 20 CNY'))

    await unmount(rendered)
  })

  test('hides an unprovable preset discount breakdown', async () => {
    await i18n.changeLanguage('en')
    setCnyBillingAndPaymentDisplay()
    const rendered = await render(
      <RechargeFormCard
        topupInfo={topupInfo}
        presetAmounts={[{ value: 50000000, discount: Number.NaN }]}
        selectedPreset={50000000}
        onSelectPreset={() => undefined}
        topupAmount={50000000}
        onTopupAmountChange={() => undefined}
        paymentAmount={80}
        calculating={false}
        onPaymentMethodSelect={() => undefined}
        paymentLoading={null}
        redemptionCode=''
        onRedemptionCodeChange={() => undefined}
        onRedeem={() => undefined}
        redeeming={false}
        priceRatio={1}
      />
    )

    const text = rendered.container.textContent ?? ''
    assert.equal(
      text.includes(
        'Selected method: Alipay · Amount due: 80 CNY (actual payment)'
      ),
      true
    )
    assert.equal(text.includes('(original'), false)
    assert.equal(text.includes('Discount applied'), false)

    await unmount(rendered)
  })

  test('shows only the localized platform marker for custom credit', async () => {
    await i18n.changeLanguage('en')
    setUsdBillingCurrency()
    const rendered = await render(
      <RechargeFormCard
        topupInfo={topupInfo}
        presetAmounts={[]}
        selectedPreset={null}
        onSelectPreset={() => undefined}
        topupAmount={500000}
        onTopupAmountChange={() => undefined}
        paymentAmount={0.14}
        calculating={false}
        onPaymentMethodSelect={() => undefined}
        paymentLoading={null}
        redemptionCode=''
        onRedemptionCodeChange={() => undefined}
        onRedeem={() => undefined}
        redeeming={false}
        priceRatio={0.14}
      />
    )

    assert.equal(
      rendered.container.querySelector('label[for="topup-amount"]')
        ?.textContent,
      'Top-up amount (USD)'
    )
    assert.equal(
      rendered.container.querySelector('#topup-amount')?.getAttribute('value'),
      '1'
    )
    assert.deepEqual(
      [
        ...rendered.container.querySelectorAll(
          '[data-slot="input-group-addon"]'
        ),
      ]
        .map((addon) => addon.textContent)
        .slice(0, 2),
      ['USD?']
    )
    assert.equal(
      rendered.container.textContent?.includes(
        'Selected method: Alipay · Amount due: 0.14 CNY (actual payment)'
      ),
      true
    )

    await unmount(rendered)
  })

  test('blocks non-fiat Linux.do quotes and confirmation without relabelling them', async () => {
    await i18n.changeLanguage('en')
    setUsdBillingCurrency()
    const paymentMethod = {
      name: 'LINUX DO Credit',
      settlement_unit: 'LDC',
      topup_ratio: '0.5',
      type: 'epay',
      min_topup_credit: '0',
      unit_price: '10',
    }
    const recharge = await render(
      <RechargeFormCard
        topupInfo={{
          ...topupInfo,
          discount: { 500000: 0.8 },
          pay_methods: [paymentMethod],
          topup_group_ratio: 0.14,
        }}
        presetAmounts={[{ value: 500000, discount: 0.8 }]}
        selectedPreset={500000}
        onSelectPreset={() => undefined}
        topupAmount={500000}
        onTopupAmountChange={() => undefined}
        paymentAmount={0.56}
        calculating={false}
        onPaymentMethodSelect={() => undefined}
        paymentLoading={null}
        redemptionCode=''
        onRedemptionCodeChange={() => undefined}
        onRedeem={() => undefined}
        redeeming={false}
      />
    )

    assert.ok(recharge.container.textContent?.includes('Payment unavailable'))
    assert.equal(recharge.container.textContent?.includes('0.56 LDC'), false)
    assert.equal(
      recharge.container.textContent?.includes('10 LDC / 1 USD'),
      false
    )
    const methodButton = recharge.container.querySelector<HTMLButtonElement>(
      'button[aria-label="LINUX DO Credit. Payment unavailable"]'
    )
    assert.ok(methodButton?.disabled)
    await unmount(recharge)

    const confirmation = await render(
      <PaymentConfirmDialog
        open
        onOpenChange={() => undefined}
        onConfirm={() => undefined}
        topupAmount={500000}
        paymentAmount={0.56}
        paymentMethod={paymentMethod}
        calculating={false}
        processing={false}
        discountRate={0.8}
      />
    )

    assert.ok(document.body.textContent?.includes('Payment unavailable'))
    assert.equal(document.body.textContent?.includes('0.56 LDC'), false)
    const confirm = [
      ...document.querySelectorAll<HTMLButtonElement>(
        '[role="alertdialog"] button'
      ),
    ].find((button) => button.textContent?.includes('Confirm Payment'))
    assert.ok(confirm?.disabled)
    await unmount(confirmation)
  })

  test('disables a payment method above its credited balance limit', async () => {
    await i18n.changeLanguage('en')
    setUsdBillingCurrency()
    const rendered = await render(
      <RechargeFormCard
        topupInfo={{
          ...topupInfo,
          pay_methods: [
            {
              name: 'LINUX DO Credit',
              type: 'epay',
              min_topup_credit: '0',
              max_topup: '20',
              max_topup_amount: '20',
              max_topup_credit: '10000000',
            },
          ],
        }}
        presetAmounts={[]}
        selectedPreset={null}
        onSelectPreset={() => undefined}
        topupAmount={12500000}
        onTopupAmountChange={() => undefined}
        paymentAmount={25}
        calculating={false}
        onPaymentMethodSelect={() => undefined}
        paymentLoading={null}
        redemptionCode=''
        onRedemptionCodeChange={() => undefined}
        onRedeem={() => undefined}
        redeeming={false}
      />
    )

    const methodButton = [
      ...rendered.container.querySelectorAll('button'),
    ].find((button) => button.textContent?.includes('LINUX DO Credit'))
    assert.equal(methodButton?.disabled, true)
    assert.equal(methodButton?.textContent?.includes('Maximum: 20'), true)
    assert.equal(
      methodButton?.getAttribute('title'),
      'Maximum credited balance per payment: 20 USD'
    )

    await unmount(rendered)
  })

  test('uses the server request-amount cap instead of USD or gateway pricing', async () => {
    await i18n.changeLanguage('en')
    setCnyBillingAndPaymentDisplay()
    // The server uses 6.8 platform units/USD for limits. A custom gateway
    // can price those units differently without changing the credited cap.
    for (const amount of [17, 18]) {
      let selected = false
      const method = {
        name: 'Limited custom gateway',
        type: 'epay',
        min_topup_credit: '0',
        settlement_currency: 'EUR',
        platform_units_per_usd: '99',
        settlement_units_per_usd: '10',
        max_topup: '2.5',
        max_topup_amount: '17',
        max_topup_credit: '8500000',
      }
      const rendered = await render(
        <RechargeFormCard
          topupInfo={{ ...topupInfo, pay_methods: [method] }}
          presetAmounts={[]}
          selectedPreset={null}
          onSelectPreset={() => undefined}
          topupAmount={amount * 500000}
          onTopupAmountChange={() => undefined}
          paymentAmount={1}
          calculating={false}
          onPaymentMethodSelect={() => {
            selected = true
          }}
          paymentLoading={null}
          redemptionCode=''
          onRedemptionCodeChange={() => undefined}
          onRedeem={() => undefined}
          redeeming={false}
        />
      )
      const button = [...rendered.container.querySelectorAll('button')].find(
        (item) => item.textContent?.includes('Limited custom gateway')
      )
      assert.ok(button)
      assert.equal(button.disabled, amount > 17)
      if (amount === 17) {
        await act(async () => button.click())
        assert.equal(selected, true)
      } else {
        assert.equal(
          button.title,
          'Maximum credited balance per payment: 17 CNY'
        )
      }
      await unmount(rendered)
    }
  })

  test('leaves legacy caps to the server when the request-amount limit is absent', async () => {
    await i18n.changeLanguage('en')
    const rendered = await render(
      <RechargeFormCard
        topupInfo={{
          ...topupInfo,
          pay_methods: [
            {
              name: 'Legacy gateway',
              type: 'epay',
              min_topup_credit: '0',
              max_topup: '2.5',
            },
          ],
        }}
        presetAmounts={[]}
        selectedPreset={null}
        onSelectPreset={() => undefined}
        topupAmount={8500000}
        onTopupAmountChange={() => undefined}
        paymentAmount={1}
        calculating={false}
        onPaymentMethodSelect={() => undefined}
        paymentLoading={null}
        redemptionCode=''
        onRedemptionCodeChange={() => undefined}
        onRedeem={() => undefined}
        redeeming={false}
      />
    )
    const button = [...rendered.container.querySelectorAll('button')].find(
      (item) => item.textContent?.includes('Legacy gateway')
    )
    assert.equal(button?.disabled, false)
    await unmount(rendered)
  })

  test('applies the current group multiplier to ordinary payment presets', async () => {
    await i18n.changeLanguage('en')
    setUsdBillingCurrency()
    const rendered = await render(
      <RechargeFormCard
        topupInfo={{ ...topupInfo, topup_group_ratio: 0.14 }}
        presetAmounts={[{ value: 500000 }]}
        selectedPreset={500000}
        onSelectPreset={() => undefined}
        topupAmount={500000}
        onTopupAmountChange={() => undefined}
        paymentAmount={0.14}
        calculating={false}
        onPaymentMethodSelect={() => undefined}
        paymentLoading={null}
        redemptionCode=''
        onRedemptionCodeChange={() => undefined}
        onRedeem={() => undefined}
        redeeming={false}
        priceRatio={1}
      />
    )

    assert.equal(
      rendered.container.textContent?.includes(
        'Selected method: Alipay · Amount due: 0.14 CNY (actual payment)'
      ),
      true
    )
    assert.equal(
      rendered.container.textContent?.includes('5.4 CNY / 1 USD'),
      true
    )
    await unmount(rendered)
  })

  test('repeats the destination, credited balance, top-up payment, and method in confirmation', async () => {
    await i18n.changeLanguage('en')
    setUsdBillingCurrency()
    const rendered = await render(
      <PaymentConfirmDialog
        open
        onOpenChange={() => undefined}
        onConfirm={() => undefined}
        topupAmount={500000}
        paymentAmount={0.15}
        paymentMethod={{ name: 'Alipay', type: 'alipay' }}
        calculating={false}
        processing={false}
        discountRate={1}
      />
    )

    const pageText = document.body.textContent ?? ''
    assert.equal(
      pageText.includes('Current signed-in account · API usage balance'),
      true
    )
    assert.equal(pageText.includes('Destination'), true)
    assert.equal(pageText.includes('Balance credited'), true)
    assert.equal(pageText.includes('You top up'), true)
    assert.equal(pageText.includes('1 USD?'), true)
    assert.equal(pageText.includes('(Platform)'), false)
    assert.equal(pageText.includes('$'), false)
    assert.equal(pageText.includes('0.15 USD'), true)
    assert.equal(pageText.includes('Alipay'), true)
    const confirmationContent = document.querySelector(
      '[data-slot="alert-dialog-content"]'
    )
    assert.equal(
      confirmationContent?.classList.contains('max-h-[calc(100dvh-2rem)]'),
      true
    )
    assert.equal(
      confirmationContent?.classList.contains('overflow-y-auto'),
      true
    )

    await unmount(rendered)
  })

  test('orders configured presets by raw amount without changing their discounts or source array', async () => {
    await i18n.changeLanguage('en')
    setCnyBillingAndPaymentDisplay()
    useSystemConfigStore.setState((state) => ({
      config: {
        ...state.config,
        currency: {
          ...state.config.currency,
          cnyPerUsd: 6.71436,
          cnyPerUsdExact: '6.71436',
        },
      },
    }))
    const amounts = [1, 2, 50, 5, 10, 20, 100, 500].map(
      (amount) => amount * 500000
    )
    const discounts = { 25000000: 0.95, 50000000: 0.8, 250000000: 0.7 }
    const presets = mergePresetAmounts(amounts, discounts)
    const originalPresets = presets.map((preset) => ({ ...preset }))
    presets.forEach((preset) => Object.freeze(preset))
    Object.freeze(presets)
    const selected: typeof presets = []
    const rendered = await render(
      <RechargeFormCard
        topupInfo={topupInfo}
        presetAmounts={presets}
        selectedPreset={null}
        onSelectPreset={(preset) => selected.push(preset)}
        topupAmount={500000}
        onTopupAmountChange={() => undefined}
        paymentAmount={6.71436}
        calculating={false}
        onPaymentMethodSelect={() => undefined}
        paymentLoading={null}
        redemptionCode=''
        onRedemptionCodeChange={() => undefined}
        onRedeem={() => undefined}
        redeeming={false}
      />
    )
    const cards = [
      ...rendered.container.querySelectorAll<HTMLButtonElement>(
        'button[aria-pressed]'
      ),
    ]
    assert.deepEqual(
      cards.map(
        (card) =>
          card.querySelector('[data-slot="wallet-credit-value"]')?.textContent
      ),
      [
        '6.71 CNY',
        '13.43 CNY',
        '33.57 CNY',
        '67.14 CNY',
        '134.29 CNY',
        '335.72 CNY',
        '671.44 CNY',
        '3,357.18 CNY',
      ]
    )
    assert.deepEqual(
      cards.map(
        (card) => card.querySelector('[data-slot="badge"]')?.textContent
      ),
      [
        undefined,
        undefined,
        undefined,
        undefined,
        undefined,
        '5% off',
        '20% off',
        '30% off',
      ]
    )
    for (const card of cards) await act(async () => card.click())
    assert.deepEqual(
      selected.map(({ value, discount }) => [value, discount]),
      [
        [500000, 1],
        [1000000, 1],
        [2500000, 1],
        [5000000, 1],
        [10000000, 1],
        [25000000, 0.95],
        [50000000, 0.8],
        [250000000, 0.7],
      ]
    )
    assert.ok(selected.every((preset) => presets.includes(preset)))
    assert.deepEqual(presets, originalPresets)
    await unmount(rendered)
  })

  test('keeps all eight Chinese presets in a 390px viewport without showing stale preset details', async () => {
    await i18n.changeLanguage('zh')
    setCnyBillingAndPaymentDisplay()
    const rendered = await render(
      <RechargeFormCard
        presetAmounts={[1, 2, 5, 10, 20, 50, 100, 500].map((value) => ({
          value: value * 500000,
        }))}
        selectedPreset={50000000}
        onSelectPreset={() => undefined}
        topupAmount={500000}
        onTopupAmountChange={() => undefined}
        paymentAmount={0.14}
        calculating={false}
        onPaymentMethodSelect={() => undefined}
        paymentLoading={null}
        redemptionCode=''
        onRedemptionCodeChange={() => undefined}
        onRedeem={() => undefined}
        redeeming={false}
        topupInfo={{
          ...topupInfo,
          discount: { 25000000: 0.95, 50000000: 0.8, 250000000: 0.7 },
          enable_waffo_pancake_topup: true,
          pay_methods: [
            ...topupInfo.pay_methods,
            { name: 'Waffo Pancake', type: 'waffo_pancake' },
          ],
        }}
        priceRatio={1}
      />
    )

    rendered.container.style.width = '390px'
    const cards = [
      ...rendered.container.querySelectorAll<HTMLButtonElement>(
        'button[aria-pressed]'
      ),
    ]
    assert.equal(cards.length, 8)
    assert.equal(
      cards.every((card) => card.scrollWidth <= card.clientWidth),
      true
    )
    assert.equal(
      cards.some((card) =>
        card
          .querySelector('[data-slot="wallet-credit-value"]')
          ?.textContent?.trim()
          .startsWith('100')
      ),
      true
    )
    assert.equal(rendered.container.textContent?.includes('$'), false)
    assert.equal(
      rendered.container.textContent?.includes(
        '卡片中的金额是平台到账金额，实际支付金额和优惠会根据所选支付方式计算。'
      ),
      false
    )
    assert.equal(
      rendered.container.textContent?.includes(
        '所选方式：Alipay · 预计支付：432 CNY（原价 540 CNY）'
      ),
      false
    )
    assert.equal(
      cards
        .find((card) =>
          card
            .querySelector('[data-slot="wallet-credit-value"]')
            ?.textContent?.trim()
            .startsWith('100')
        )
        ?.getAttribute('aria-pressed'),
      'false'
    )
    assert.equal(
      rendered.container.textContent?.includes('平台优惠 20%'),
      false
    )
    assert.equal(
      rendered.container.textContent?.includes('已优惠 108 CNY'),
      false
    )
    assert.equal(
      rendered.container.textContent?.includes(
        '所选方式：Alipay · 待支付金额：0.14 CNY（实际付款）'
      ),
      true
    )
    assert.equal(rendered.container.textContent?.includes('平台优惠 0%'), false)
    assert.equal(
      cards.some((card) => card.textContent?.includes('平台优惠 0%') ?? false),
      false
    )
    assert.equal(
      rendered.container
        .querySelector('#topup-amount')
        ?.getAttribute('aria-label'),
      '充值金额 (CNY)'
    )
    assert.equal(
      rendered.container.textContent?.includes(
        'Waffo Pancake 当前仅支持 USD，请将该网关货币设为 USD。'
      ),
      false
    )

    await unmount(rendered)
    await i18n.changeLanguage('en')
  })

  test('keeps Chinese payment units bound to the selected gateway', async () => {
    await i18n.changeLanguage('zh')
    setUsdBillingCurrency()
    const rendered = await render(
      <RechargeFormCard
        topupInfo={{ ...topupInfo, discount: { 50000000: 0.8 } }}
        presetAmounts={[{ value: 50000000, discount: 0.8 }]}
        selectedPreset={50000000}
        onSelectPreset={() => undefined}
        topupAmount={50000000}
        onTopupAmountChange={() => undefined}
        paymentAmount={80}
        paymentCurrency='CNY'
        paymentDiscount={parsePaymentDiscount(
          {
            schema_version: 1,
            currency: 'CNY',
            basis: 'amount_preset_and_code',
            original_amount: '100.00',
            paid_amount: '80.00',
            savings_amount: '20.00',
            discount_percent: '20.00',
          },
          '80.00',
          'CNY'
        )}
        calculating={false}
        onPaymentMethodSelect={() => undefined}
        paymentLoading={null}
        redemptionCode=''
        onRedemptionCodeChange={() => undefined}
        onRedeem={() => undefined}
        redeeming={false}
        priceRatio={1}
      />
    )

    const text = rendered.container.textContent ?? ''
    assert.equal(
      text.includes('通过 Alipay 预计实付：$80 USD（原价需付款 $100 USD）'),
      false
    )
    assert.equal(
      text.includes('所选方式：Alipay · 待支付金额：80 CNY（实际付款）'),
      true
    )
    assert.equal(text.includes('已优惠 20 CNY'), true)
    assert.equal(text.includes('人民币'), false)

    await unmount(rendered)
    await i18n.changeLanguage('en')
  })

  test('PaymentConfirmDialog renders discount code savings and strikethrough for settlement quotes', async () => {
    const rendered = await render(
      <PaymentConfirmDialog
        open
        onOpenChange={() => undefined}
        onConfirm={() => undefined}
        topupAmount={50000000}
        paymentAmount={8.47}
        paymentCurrency='USD'
        paymentDiscount={parsePaymentDiscount(
          {
            schema_version: 1,
            currency: 'USD',
            basis: 'amount_preset_and_code',
            original_amount: '14.12',
            paid_amount: '8.4700',
            savings_amount: '5.65',
            discount_percent: '40.01',
          },
          '8.4700',
          'USD'
        )}
        settlementQuote={{ amount: '8.4700', currency: 'USD' }}
        paymentMethod={{
          name: 'Waffo Pancake',
          type: 'waffo_pancake',
          min_topup_credit: '0',
        }}
        calculating={false}
        processing={false}
        discountCode='SAVE40'
        discountPercent={40}
      />
    )

    const text =
      document.querySelector('[role="alertdialog"]')?.textContent ?? ''
    assert.ok(
      text.includes('8.4700 USD'),
      'actual payment quote should be rendered'
    )
    assert.ok(text.includes('SAVE40'), 'discount code should be displayed')
    assert.ok(
      text.includes('Discount applied: 40.01% off'),
      'discount percent should be displayed'
    )
    assert.ok(text.includes('You save'), 'savings line should be displayed')
    assert.ok(
      text.includes('14.12 USD'),
      'pre-discount strikethrough amount should be rendered'
    )
    assert.ok(
      text.includes('5.65 USD'),
      'discount code savings amount should be rendered'
    )

    await unmount(rendered)
  })
})

test('one Credit survives currency switches and quote requests keep their original denomination', async () => {
  await i18n.changeLanguage('en')
  useWalletCurrencyPreferenceStore.getState().setPreference('CREDIT')
  useSystemConfigStore.setState((state) => ({
    config: {
      ...state.config,
      currency: {
        ...state.config.currency,
        currencyUnit: 'credit',
        quotaPerUnit: 500000,
        creditsPerUsd: 500000,
        creditsPerUsdExact: '500000',
        cnyPerUsd: 6.8,
        cnyPerUsdExact: '6.8',
      },
    },
  }))
  const requests: Array<Record<string, unknown>> = []
  api.post = (async (_url: string, body: Record<string, unknown>) => {
    requests.push(body)
    return {
      data: {
        success: true,
        data: '0.01',
        ...creditGrant(Number(body.amount), 500000),
        settlement_currency: 'USD',
      },
    }
  }) as typeof api.post
  const { calculateCreditAmount } = await import('../api')
  const method = {
    name: 'Card',
    type: 'card',
    min_topup_credit: '0',
    settlement_currency: 'CNY',
    platform_units_per_usd: '6.8',
    settlement_units_per_usd: '1',
  }
  function Harness() {
    const [quota, setQuota] = useState(1)
    return (
      <>
        <RechargeFormCard
          topupInfo={{ ...topupInfo, min_topup: 0, pay_methods: [method] }}
          presetAmounts={[]}
          selectedPreset={null}
          onSelectPreset={() => undefined}
          topupAmount={quota}
          onTopupAmountChange={(value) => {
            setQuota(value)
            void calculateCreditAmount({
              amount: value,
              payment_method: 'card',
            })
          }}
          paymentAmount={0.01}
          paymentCurrency='USD'
          selectedPaymentMethod={method}
          calculating={false}
          onPaymentMethodSelect={() => undefined}
          paymentLoading={null}
          redemptionCode=''
          onRedemptionCodeChange={() => undefined}
          onRedeem={() => undefined}
          redeeming={false}
        />
        <PaymentConfirmDialog
          open
          onOpenChange={() => undefined}
          onConfirm={() => undefined}
          topupAmount={quota}
          creditedQuota={1}
          paymentAmount={0.01}
          paymentCurrency='USD'
          paymentMethod={method}
          calculating={false}
          processing={false}
        />
      </>
    )
  }
  const rendered = await render(<Harness />)
  const input =
    rendered.container.querySelector<HTMLInputElement>('#topup-amount')
  assert.ok(input)
  assert.equal(input.value, '0.000002')
  assert.equal(document.body.textContent?.includes('1 Credits'), false)
  const originalInput = input.value
  for (const preference of ['USD', 'CNY', 'CREDIT'] as const) {
    await act(async () => {
      useWalletCurrencyPreferenceStore.getState().setPreference(preference)
    })
    assert.equal(
      input.value,
      originalInput,
      'balance display never changes payment input'
    )
    assert.ok(
      document.body.textContent?.includes('0.01 USD'),
      'checkout fiat remains USD'
    )
    assert.equal(
      requests.length,
      0,
      'display preferences do not requote raw selection'
    )
  }
  await editInput(input, '0.000004')
  assert.deepEqual(requests, [
    {
      amount: 2,
      payment_method: 'card',
      amount_unit: 'LEDGER_QUOTA',
      credit_metadata_version: 2,
    },
  ])
  await act(async () => {
    useWalletCurrencyPreferenceStore.getState().setPreference('CNY')
  })
  assert.equal(input.value, '0.000004')
  await editInput(input, '1')
  assert.deepEqual(requests.at(-1), {
    amount: 500000,
    payment_method: 'card',
    amount_unit: 'LEDGER_QUOTA',
    credit_metadata_version: 2,
  })
  await act(async () => {
    useWalletCurrencyPreferenceStore.getState().setPreference('CREDIT')
  })
  assert.equal(input.value, '1')
  assert.equal(requests.length, 2)
  assert.ok(document.body.textContent?.includes('0.01 USD'))
  await unmount(rendered)
})

test('a one-Credit private transfer keeps raw quota when its display currency changes', async () => {
  await i18n.changeLanguage('en')
  useWalletCurrencyPreferenceStore.getState().setPreference('CREDIT')
  const { WalletTransfers } = await import('../transfers/wallet-transfers')
  const bodies: Array<Record<string, unknown>> = []
  api.get = (async () => ({
    data: { success: true, data: [] },
  })) as typeof api.get
  api.post = (async (url, body: Record<string, unknown>) => {
    assert.equal(url, '/api/wallet-transfer')
    bodies.push(body)
    return {
      data: {
        success: true,
        data: {
          id: 1,
          token: 'a'.repeat(64),
          quota: 1,
          status: 'pending',
          created_at: 1,
          claimed_at: 0,
          cancelled_at: 0,
          recipient_id: 0,
          recipient_username: '',
          recipient_name: '',
          recipient_email: '',
        },
      },
    }
  }) as typeof api.post
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const rendered = await render(
    <QueryClientProvider client={client}>
      <WalletTransfers
        userID={7}
        balance={5}
        onBalanceChange={async () => undefined}
      />
    </QueryClientProvider>
  )
  const open = [...rendered.container.querySelectorAll('button')].find(
    (button) => button.textContent?.trim() === 'Transfer'
  )
  assert.ok(open)
  await act(async () => {
    open.click()
    await flushEffects()
  })
  const input = document.querySelector<HTMLInputElement>('#transfer-amount')
  assert.ok(input)
  const create = [...document.querySelectorAll('button')].find(
    (button) => button.textContent?.trim() === 'Create transfer link'
  )
  assert.ok(create)
  await editInput(input, '1.5')
  assert.equal(
    create.disabled,
    true,
    'fractional raw Credits are not silently rounded'
  )
  await editInput(input, '1')
  assert.equal(create.disabled, false)
  await act(async () =>
    useWalletCurrencyPreferenceStore.getState().setPreference('USD')
  )
  assert.equal(input.value, '0.000002')
  await act(async () =>
    useWalletCurrencyPreferenceStore.getState().setPreference('CREDIT')
  )
  assert.equal(input.value, '1')
  assert.equal(bodies.length, 0)
  await act(async () => {
    create.click()
    await flushEffects()
  })
  assert.equal(bodies.length, 1)
  assert.equal(bodies[0].quota, 1)
  assert.equal(typeof bodies[0].request_key, 'string')
  assert.equal(Object.hasOwn(bodies[0], 'amount'), false)
  await unmount(rendered)
  client.clear()
})

for (const scenario of [
  { currency: 'USD', initial: 1000000, expected: 1500000 },
  { currency: 'CNY', initial: 1, expected: 62501 },
] as const) {
  test(`amount arrows add to raw credit without round-tripping current ${scenario.currency} float`, async () => {
    await i18n.changeLanguage('en')
    useWalletCurrencyPreferenceStore.getState().setPreference(scenario.currency)
    paymentDisplayFixture = scenario.currency
    useSystemConfigStore.setState((state) => ({
      config: {
        ...state.config,
        currency: {
          ...state.config.currency,
          creditsPerUsd: 500000,
          creditsPerUsdExact: '500000',
          cnyPerUsd: 8,
          cnyPerUsdExact: '8',
        },
      },
    }))
    const changes: number[] = []
    function Harness() {
      const [quota, setQuota] = useState<number>(scenario.initial)
      return (
        <RechargeFormCard
          topupInfo={{ ...topupInfo, min_topup: 1 }}
          presetAmounts={[]}
          selectedPreset={null}
          onSelectPreset={() => undefined}
          topupAmount={quota}
          onTopupAmountChange={(value) => {
            changes.push(value)
            setQuota(value)
          }}
          paymentAmount={1}
          calculating={false}
          onPaymentMethodSelect={() => undefined}
          paymentLoading={null}
          redemptionCode=''
          onRedemptionCodeChange={() => undefined}
          onRedeem={() => undefined}
          redeeming={false}
        />
      )
    }
    const rendered = await render(<Harness />)
    const increase = rendered.container.querySelector<HTMLButtonElement>(
      'button[aria-label="Increase amount"]'
    )
    assert.ok(increase)
    const event = new Event('keydown', { bubbles: true })
    Object.defineProperty(event, 'key', { value: 'Enter' })
    await act(async () => increase.dispatchEvent(event))
    assert.deepEqual(changes, [scenario.expected])
    await unmount(rendered)
  })
}

test('raw min and max limits keep a one-Credit difference near MAX_SAFE_INTEGER', async () => {
  await i18n.changeLanguage('en')
  useWalletCurrencyPreferenceStore.getState().setPreference('CREDIT')
  for (const quota of [9007199254740986, 9007199254740987]) {
    const method = {
      name: 'Exact limit',
      type: 'card',
      min_topup_credit: '1',
      max_topup_credit: '9007199254740986',
    }
    const rendered = await render(
      <RechargeFormCard
        topupInfo={{ ...topupInfo, min_topup: 1, pay_methods: [method] }}
        presetAmounts={[]}
        selectedPreset={null}
        onSelectPreset={() => undefined}
        topupAmount={quota}
        onTopupAmountChange={() => undefined}
        paymentAmount={1}
        calculating={false}
        onPaymentMethodSelect={() => undefined}
        paymentLoading={null}
        redemptionCode=''
        onRedemptionCodeChange={() => undefined}
        onRedeem={() => undefined}
        redeeming={false}
      />
    )
    const button = [...rendered.container.querySelectorAll('button')].find(
      (item) => item.textContent?.includes('Exact limit')
    )
    assert.ok(button)
    assert.equal(button.disabled, quota > 9007199254740986)
    await unmount(rendered)
  }
})

test('each gateway uses its complete raw minimum without another provider minimum', async () => {
  await i18n.changeLanguage('en')
  const epay = {
    name: 'Higher minimum',
    type: 'epay',
    min_topup_credit: '5000000',
  }
  const stripe = {
    name: 'One Credit card',
    type: 'stripe',
    min_topup_credit: '1',
  }
  const rendered = await render(
    <RechargeFormCard
      topupInfo={{
        ...topupInfo,
        enable_stripe_topup: true,
        min_topup: 5000000,
        pay_methods: [epay, stripe],
      }}
      presetAmounts={[]}
      selectedPreset={null}
      onSelectPreset={() => undefined}
      topupAmount={1}
      onTopupAmountChange={() => undefined}
      paymentAmount={1}
      calculating={false}
      onPaymentMethodSelect={() => undefined}
      paymentLoading={null}
      redemptionCode=''
      onRedemptionCodeChange={() => undefined}
      onRedeem={() => undefined}
      redeeming={false}
    />
  )
  const buttons = [...rendered.container.querySelectorAll('button')]
  assert.equal(
    buttons.find((button) => button.textContent?.includes('Higher minimum'))
      ?.disabled,
    true
  )
  assert.equal(
    buttons.find((button) => button.textContent?.includes('One Credit card'))
      ?.disabled,
    false
  )
  await unmount(rendered)
})

test('dedicated Waffo enforces complete 3.5M / 8.75M raw limits at a 300k legacy batch', async () => {
  await i18n.changeLanguage('en')
  useWalletCurrencyPreferenceStore.getState().setPreference('CREDIT')
  useSystemConfigStore.setState((state) => ({
    config: {
      ...state.config,
      currency: {
        ...state.config.currency,
        quotaPerUnit: 300000,
        creditsPerUsd: 500000,
        creditsPerUsdExact: '500000',
      },
    },
  }))
  for (const raw of [3499999, 3500000, 8750000, 8750001]) {
    const rendered = await render(
      <RechargeFormCard
        topupInfo={{
          ...topupInfo,
          enable_online_topup: false,
          enable_waffo_topup: true,
          waffo_pay_methods: [{ name: 'Waffo card' }],
          waffo_min_topup: 900000,
          waffo_credit_min_topup: 3500000,
          waffo_credit_max_topup: 8750000,
        }}
        presetAmounts={[]}
        selectedPreset={null}
        onSelectPreset={() => undefined}
        topupAmount={raw}
        onTopupAmountChange={() => undefined}
        paymentAmount={1}
        calculating={false}
        onPaymentMethodSelect={() => undefined}
        paymentLoading={null}
        redemptionCode=''
        onRedemptionCodeChange={() => undefined}
        onRedeem={() => undefined}
        redeeming={false}
        onWaffoMethodSelect={() => undefined}
      />
    )
    const button = [...rendered.container.querySelectorAll('button')].find(
      (item) => item.textContent?.includes('Waffo card')
    )
    assert.ok(button)
    assert.equal(button.disabled, raw < 3500000 || raw > 8750000)
    await unmount(rendered)
  }
})
