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
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

import enLocale from '@/i18n/locales/en.json'
import frLocale from '@/i18n/locales/fr.json'
import jaLocale from '@/i18n/locales/ja.json'
import ruLocale from '@/i18n/locales/ru.json'
import viLocale from '@/i18n/locales/vi.json'
import zhTWLocale from '@/i18n/locales/zh-TW.json'
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
    zh: { translation: zhLocale.translation },
  },
})

const { useEffect } = await import('react')
const { RechargeFormCard } = await import('./recharge-form-card')
const { Wallet } = await import('../index')
const { useTopupInfo } = await import('../hooks/use-topup-info')
const { api } = await import('@/lib/api')
const { useAuthStore } = await import('@/stores/auth-store')
const { useSystemConfigStore } = await import('@/stores/system-config-store')
const { PaymentConfirmDialog } =
  await import('./dialogs/payment-confirm-dialog')
const { formatCreditBalance, formatPaymentAmount } = await import('../lib')

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const originalConfig = useSystemConfigStore.getState().config
const originalGet = api.get
const originalPost = api.post
// oxlint-disable-next-line no-console -- The test captures and restores the expected production error log.
const originalConsoleError = console.error

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

function setCnyBillingCurrency() {
  useSystemConfigStore.setState((state) => ({
    config: {
      ...state.config,
      currency: {
        ...state.config.currency,
        quotaDisplayType: 'CNY',
        usdExchangeRate: 7,
      },
    },
  }))
}

function setUsdBillingCurrency() {
  useSystemConfigStore.setState((state) => ({
    config: {
      ...state.config,
      currency: {
        ...state.config.currency,
        quotaDisplayType: 'USD',
      },
    },
  }))
}

type Rendered = {
  container: HTMLDivElement
  root: ReturnType<typeof createRoot>
}

async function render(node: React.ReactNode): Promise<Rendered> {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  await act(async () => {
    root.render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>)
  })

  return { container, root }
}

async function unmount(rendered: Rendered) {
  await act(async () => rendered.root.unmount())
  rendered.container.remove()
}

afterEach(() => {
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

const topupInfo = {
  enable_online_topup: true,
  enable_stripe_topup: false,
  pay_methods: [
    {
      name: 'Alipay',
      type: 'alipay',
      settlement_unit: 'CNY',
      unit_price: '5.4',
    },
  ],
  min_topup: 10,
  stripe_min_topup: 10,
  amount_options: [100],
  discount: {},
}

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

  test('keeps both platform credit labels non-fiat in every locale', () => {
    const locales = [
      ['en', enLocale],
      ['zh', zhLocale],
      ['zh-TW', zhTWLocale],
      ['fr', frLocale],
      ['ja', jaLocale],
      ['ru', ruLocale],
      ['vi', viLocale],
    ] as const

    for (const [locale, messages] of locales) {
      for (const key of [
        'Platform credit',
        'Custom platform credit',
      ] as const) {
        const value = messages.translation[key]
        assert.equal(value.includes('$'), false, `${locale}: ${key}`)
      }
    }
  })

  test('renders Chinese platform title, preset card, and input addon without a dollar symbol', async () => {
    await i18n.changeLanguage('zh')
    setCnyBillingCurrency()

    assert.equal(formatCreditBalance(6.8), '6.8 (Platform)')
    assert.equal(formatCreditBalance(Number.NaN), '-')
    assert.equal(formatCreditBalance(6.8).includes('$'), false)
    assert.equal(formatPaymentAmount(1, 'USD'), '1 USD')
    assert.equal(formatPaymentAmount(6.8, 'CNY'), '6.8 CNY')

    const rendered = await render(
      <RechargeFormCard
        topupInfo={topupInfo}
        presetAmounts={[{ value: 10 }]}
        selectedPreset={null}
        onSelectPreset={() => undefined}
        topupAmount={10}
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
        (label) => label.textContent === '平台额度'
      ),
      true
    )
    assert.equal(
      rendered.container.querySelector('label[for="topup-amount"]')
        ?.textContent,
      '自定义平台额度'
    )
    assert.equal(presetCard?.textContent?.includes('10 (平台)'), true)
    assert.equal(presetCard?.textContent?.includes('$'), false)
    assert.deepEqual(
      addons.map((addon) => addon.textContent),
      ['(平台)']
    )
    assert.equal(
      addons.some((addon) => addon.textContent?.includes('$')),
      false
    )
    assert.equal(text.includes('$'), false)
    await unmount(rendered)
  })

  test('hides a stale payment quote while a fresh quote is calculating', async () => {
    await i18n.changeLanguage('en')
    setUsdBillingCurrency()
    const rendered = await render(
      <RechargeFormCard
        topupInfo={topupInfo}
        presetAmounts={[{ value: 10 }]}
        selectedPreset={10}
        onSelectPreset={() => undefined}
        topupAmount={10}
        onTopupAmountChange={() => undefined}
        paymentAmount={236.11}
        selectedPaymentMethod={{
          name: 'Waffo Pancake',
          type: 'waffo_pancake',
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
          topupAmount={10}
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
          topupAmount={10}
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
      assert.equal(confirmationText.includes('0 USD'), false)
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
          topupAmount={1}
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
    setCnyBillingCurrency()
    const paymentMethod = {
      name: 'USD card',
      type: 'card',
      settlement_currency: 'USD',
      platform_units_per_usd: '6.8',
      settlement_units_per_usd: '1',
      min_topup: 6.8,
      max_topup: '68',
    }
    const recharge = await render(
      <RechargeFormCard
        topupInfo={{
          ...topupInfo,
          min_topup: 6.8,
          pay_methods: [paymentMethod],
        }}
        presetAmounts={[{ value: 6.8 }]}
        selectedPreset={6.8}
        onSelectPreset={() => undefined}
        topupAmount={6.8}
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
        '[aria-label="Preset amount: 6.8 (Platform). Actual payment: 1 USD. Original payment: 1 USD. Platform discount 0%"]'
      )
    )
    assert.equal(text.includes('6.8 (Platform)'), true)
    assert.equal(text.includes('Estimated payment: 1 USD'), true)
    assert.equal(text.includes('Amount due: 1 USD (actual payment)'), true)
    assert.equal(text.includes('$1'), false)
    assert.equal(
      recharge.container.querySelector('#topup-amount')?.getAttribute('min'),
      '6.8'
    )
    assert.equal(text.includes('1 USD / 6.8 (Platform)'), true)
    await unmount(recharge)

    const confirmation = await render(
      <PaymentConfirmDialog
        open
        onOpenChange={() => undefined}
        onConfirm={() => undefined}
        topupAmount={6.8}
        paymentAmount={1}
        paymentMethod={paymentMethod}
        calculating={false}
        processing={false}
        discountRate={1}
      />
    )
    assert.equal(
      document.body.textContent?.includes('Credit 6.8 (Platform); pay 1 USD'),
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
      settlement_currency: 'CNY',
      platform_units_per_usd: '6.8',
      settlement_units_per_usd: '6.8',
    }
    const rendered = await render(
      <RechargeFormCard
        topupInfo={{ ...topupInfo, pay_methods: [paymentMethod] }}
        presetAmounts={[{ value: 6.8 }]}
        selectedPreset={6.8}
        onSelectPreset={() => undefined}
        topupAmount={6.8}
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
        '[aria-label="Preset amount: 6.8 (Platform). Actual payment: 6.8 CNY. Original payment: 6.8 CNY. Platform discount 0%"]'
      )
    )
    assert.equal(text.includes('6.8 (Platform)'), true)
    assert.equal(text.includes('Estimated payment: 6.8 CNY'), true)
    assert.equal(text.includes('Amount due: 6.8 CNY (actual payment)'), true)
    assert.equal(text.includes('6.8 CNY / 6.8 (Platform)'), true)
    await unmount(rendered)
  })

  test('labels preset credits, payment, discount, and the custom-account destination', async () => {
    await i18n.changeLanguage('en')
    setCnyBillingCurrency()
    const rendered = await render(
      <RechargeFormCard
        topupInfo={topupInfo}
        presetAmounts={[{ value: 100 }, { value: 200, discount: 0.8 }]}
        selectedPreset={null}
        onSelectPreset={() => undefined}
        topupAmount={100}
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
      '[aria-label="Preset amount: 100 (Platform). Actual payment: 540 CNY. Original payment: 540 CNY. Platform discount 0%"]'
    )
    assert.ok(noDiscountPreset)
    assert.equal(
      noDiscountPreset?.textContent?.includes('100 (Platform)'),
      true
    )
    assert.equal(
      noDiscountPreset?.textContent?.includes('Estimated actual payment'),
      false
    )

    const discountPreset = rendered.container.querySelector(
      '[aria-label="Preset amount: 200 (Platform). Actual payment: 864 CNY. Original payment: 1,080 CNY. Platform discount 20%. Discount applied 216 CNY"]'
    )
    assert.ok(discountPreset)
    assert.equal(
      discountPreset?.textContent?.includes('Platform discount 20%'),
      true
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
      'Custom platform credit'
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
      true
    )

    await unmount(rendered)
  })

  test('shows the prescribed 100-credit, 20%-discount payment breakdown', async () => {
    await i18n.changeLanguage('en')
    setCnyBillingCurrency()
    const rendered = await render(
      <RechargeFormCard
        topupInfo={{ ...topupInfo, discount: { 100: 0.8 } }}
        presetAmounts={[{ value: 100, discount: 0.8 }]}
        selectedPreset={100}
        onSelectPreset={() => undefined}
        topupAmount={100}
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
    assert.equal(text.includes('100 (Platform)'), true)
    assert.equal(
      text.includes(
        'Selected method: Alipay · Estimated payment: 80 CNY (original 100 CNY)'
      ),
      true
    )
    assert.equal(text.includes('Platform discount 20%'), true)
    assert.equal(text.includes('Discount applied 20 CNY'), true)

    const paymentBreakdown = text.match(
      /Estimated payment: ([\d,.]+) CNY \(original ([\d,.]+) CNY\)/
    )
    const savingsBreakdown = text.match(/Discount applied ([\d,.]+) CNY/)
    assert.ok(paymentBreakdown)
    assert.ok(savingsBreakdown)
    const actual = Number(paymentBreakdown[1]?.replaceAll(',', ''))
    const original = Number(paymentBreakdown[2]?.replaceAll(',', ''))
    const saved = Number(savingsBreakdown[1]?.replaceAll(',', ''))
    assert.equal(original - actual, saved)

    await unmount(rendered)
  })

  test('hides an unprovable preset discount breakdown', async () => {
    await i18n.changeLanguage('en')
    setCnyBillingCurrency()
    const rendered = await render(
      <RechargeFormCard
        topupInfo={topupInfo}
        presetAmounts={[{ value: 100, discount: Number.NaN }]}
        selectedPreset={100}
        onSelectPreset={() => undefined}
        topupAmount={100}
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
        topupAmount={1}
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
      'Custom platform credit'
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
      ['(Platform)']
    )
    assert.equal(
      rendered.container.textContent?.includes(
        'Selected method: Alipay · Amount due: 0.14 CNY (actual payment)'
      ),
      true
    )

    await unmount(rendered)
  })

  test('uses the initial default Linux.do method for quotes and confirmation', async () => {
    await i18n.changeLanguage('en')
    setUsdBillingCurrency()
    const paymentMethod = {
      name: 'LINUX DO Credit',
      settlement_unit: 'LDC',
      topup_ratio: '0.5',
      type: 'epay',
      unit_price: '10',
    }
    const recharge = await render(
      <RechargeFormCard
        topupInfo={{
          ...topupInfo,
          discount: { 1: 0.8 },
          pay_methods: [paymentMethod],
          topup_group_ratio: 0.14,
        }}
        presetAmounts={[{ value: 1, discount: 0.8 }]}
        selectedPreset={1}
        onSelectPreset={() => undefined}
        topupAmount={1}
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

    assert.equal(
      recharge.container.textContent?.includes(
        'Selected method: LINUX DO Credit · Estimated payment: 0.56 LDC (original 0.7 LDC)'
      ),
      true
    )
    assert.equal(
      recharge.container.textContent?.includes(
        'Selected method: LINUX DO Credit · Amount due: 0.56 LDC (actual payment)'
      ),
      true
    )
    assert.equal(
      recharge.container.textContent?.includes('10 LDC / (Platform)'),
      true
    )
    assert.equal(
      recharge.container.textContent?.includes('Channel multiplier ×0.5'),
      true
    )
    await unmount(recharge)

    const confirmation = await render(
      <PaymentConfirmDialog
        open
        onOpenChange={() => undefined}
        onConfirm={() => undefined}
        topupAmount={1}
        paymentAmount={0.56}
        paymentMethod={paymentMethod}
        calculating={false}
        processing={false}
        discountRate={0.8}
      />
    )

    assert.equal(
      document.body.textContent?.includes('Credit 1 (Platform); pay 0.56 LDC'),
      true
    )
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
              max_topup: '20',
            },
          ],
        }}
        presetAmounts={[]}
        selectedPreset={null}
        onSelectPreset={() => undefined}
        topupAmount={25}
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
    assert.equal(
      methodButton?.textContent?.includes('Maximum: 20 (Platform)'),
      true
    )
    assert.equal(
      methodButton?.getAttribute('title'),
      'Maximum platform credit per payment: 20 (Platform)'
    )

    await unmount(rendered)
  })

  test('applies the current group multiplier to ordinary payment presets', async () => {
    await i18n.changeLanguage('en')
    setUsdBillingCurrency()
    const rendered = await render(
      <RechargeFormCard
        topupInfo={{ ...topupInfo, topup_group_ratio: 0.14 }}
        presetAmounts={[{ value: 1 }]}
        selectedPreset={1}
        onSelectPreset={() => undefined}
        topupAmount={1}
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
        'Selected method: Alipay · Estimated payment: 0.14 CNY (original 0.14 CNY)'
      ),
      true
    )
    assert.equal(
      rendered.container.textContent?.includes('5.4 CNY / (Platform)'),
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
        topupAmount={1}
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
    assert.equal(pageText.includes('1 (Platform)'), true)
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

  test('keeps all eight Chinese presets in a 390px viewport without showing stale preset details', async () => {
    await i18n.changeLanguage('zh')
    setCnyBillingCurrency()
    const rendered = await render(
      <RechargeFormCard
        presetAmounts={[1, 2, 5, 10, 20, 50, 100, 500].map((value) => ({
          value,
        }))}
        selectedPreset={100}
        onSelectPreset={() => undefined}
        topupAmount={1}
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
          discount: { 50: 0.95, 100: 0.8, 500: 0.7 },
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
    assert.equal(rendered.container.textContent?.includes('100 (平台)'), true)
    assert.equal(rendered.container.textContent?.includes('$'), false)
    assert.equal(
      rendered.container.textContent?.includes(
        '卡片中的金额是平台到账金额，实际支付金额和优惠会根据所选支付方式计算。'
      ),
      true
    )
    assert.equal(
      rendered.container.textContent?.includes(
        '所选方式：Alipay · 预计支付：432 CNY（原价 540 CNY）'
      ),
      false
    )
    assert.equal(
      cards
        .find((card) => card.textContent?.includes('100 (平台)'))
        ?.getAttribute('aria-pressed'),
      'false'
    )
    assert.equal(rendered.container.textContent?.includes('平台优惠 20%'), true)
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
    assert.equal(rendered.container.textContent?.includes('平台优惠 0%'), true)
    assert.equal(
      cards.some((card) => card.textContent?.includes('平台优惠 0%') ?? false),
      false
    )
    assert.equal(
      rendered.container
        .querySelector('#topup-amount')
        ?.getAttribute('aria-label'),
      '自定义平台额度'
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
        topupInfo={{ ...topupInfo, discount: { 100: 0.8 } }}
        presetAmounts={[{ value: 100, discount: 0.8 }]}
        selectedPreset={100}
        onSelectPreset={() => undefined}
        topupAmount={100}
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
      text.includes('通过 Alipay 预计实付：$80 USD（原价需付款 $100 USD）'),
      false
    )
    assert.equal(
      text.includes('所选方式：Alipay · 预计支付：80 CNY（原价 100 CNY）'),
      true
    )
    assert.equal(text.includes('已优惠 20 CNY'), true)
    assert.equal(text.includes('人民币'), false)

    await unmount(rendered)
    await i18n.changeLanguage('en')
  })
})
