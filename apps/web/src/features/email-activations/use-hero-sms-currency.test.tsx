/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, test } from 'node:test'

import { Window } from 'happy-dom'

import type { HeroSmsSmsOffer } from './sms-api'

const dom = new Window({
  url: 'https://console.example.test/temporary-activations',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'Node',
  'Element',
  'Event',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
Object.defineProperty(globalThis, 'getComputedStyle', {
  configurable: true,
  value: dom.getComputedStyle.bind(dom),
})
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { useAuthStore } = await import('@/stores/auth-store')
const { DEFAULT_CURRENCY_CONFIG, useSystemConfigStore } =
  await import('@/stores/system-config-store')
const { useWalletCurrencyPreferenceStore } =
  await import('@/stores/wallet-currency-preference-store')
const { useHeroSmsCurrency } = await import('./use-hero-sms-currency')
const { SmsQuoteSummary, SmsPriceTierPicker } =
  await import('./sms-marketplace-sections')
const { SmsBalanceNotice } = await import('./sms-balance-notice')
const { getSmsPurchaseBalance } = await import('./sms-balance')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  fallbackLng: 'en',
  keySeparator: false,
  resources: {
    en: { translation: { Credits: 'Credits' } },
    zhCN: {
      translation: {
        Credits: '积分',
        'Maximum unit price': '报价上限',
        'Temporary SMS purchases require a balance of at least {{amount}}':
          '短信购买最低余额 {{amount}}',
        'Minimum balance: {{minimum}}. Current balance: {{balance}}.':
          '最低余额 {{minimum}}，当前 {{balance}}。',
      },
    },
  },
})
const offer: HeroSmsSmsOffer = Object.freeze({
  id: 'small-quote',
  country_id: 6,
  service: 'tg',
  operator: '',
  inventory: 3,
  customer_price_usd: '0.000011',
  charge_quota: 6,
})
const originalOffer = JSON.stringify(offer)
let root: ReturnType<typeof createRoot> | undefined
const noop = () => {}

function Charge() {
  const { formatQuota } = useHeroSmsCurrency()
  return <output data-actual-charge>{formatQuota(offer.charge_quota)}</output>
}

async function mount() {
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  const balance = getSmsPurchaseBalance(4_999_999, 500_000)
  await act(async () => {
    root?.render(
      <I18nextProvider i18n={i18n}>
        <section data-quote>
          <SmsQuoteSummary
            offer={offer}
            quantity={2}
            isFetching={false}
            isError={false}
            error={undefined}
            onRefresh={noop}
          />
        </section>
        <Charge />
        <SmsBalanceNotice
          status={balance.status}
          balanceQuota={balance.balanceQuota}
          isLoading={false}
          isRefreshing={false}
          onRefresh={noop}
        />
      </I18nextProvider>
    )
  })
  return container
}

function quoteAmounts(container: HTMLElement) {
  return [...container.querySelectorAll('[data-quote] .tabular-nums')].map(
    (node) => node.textContent
  )
}

beforeEach(async () => {
  useAuthStore.getState().auth.setUser(null)
  useWalletCurrencyPreferenceStore.getState().setPreference('')
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...DEFAULT_CURRENCY_CONFIG,
      currencyUnit: 'credit',
      creditsPerUsd: 3_500_000,
      creditsPerUsdExact: '3500000',
      cnyPerUsd: 7,
      cnyPerUsdExact: '7',
      legacyPricingUnitsPerUsd: 7,
      quotaPerUnit: 500_000,
    },
  })
  await i18n.changeLanguage('en')
})

afterEach(async () => {
  await act(async () => root?.unmount())
  root = undefined
  document.body.replaceChildren()
  assert.equal(JSON.stringify(offer), originalOffer)
})
after(() => dom.close())

test('mounted SMS quotes, integral charges and balance notice switch three currencies', async () => {
  const container = await mount()
  for (const [currency, unit, total, charge, minimum, balance] of [
    [
      'USD',
      '0.00000157 USD',
      '0.00000314 USD',
      '0.00000171 USD',
      '1.42857143 USD',
      '1.42857114 USD',
    ],
    [
      'CNY',
      '0.000011 CNY',
      '0.000022 CNY',
      '0.000012 CNY',
      '10 CNY',
      '9.999998 CNY',
    ],
    [
      'CREDIT',
      '5.5 Credits',
      '11 Credits',
      '6 Credits',
      '5,000,000 Credits',
      '4,999,999 Credits',
    ],
  ] as const) {
    await act(async () => {
      useWalletCurrencyPreferenceStore.getState().setPreference(currency)
    })
    assert.deepEqual(quoteAmounts(container), ['3', unit, total])
    assert.equal(
      container.querySelector('[data-actual-charge]')?.textContent,
      charge
    )
    assert.ok(
      container.textContent?.includes(
        `Temporary SMS purchases require a balance of at least ${minimum}`
      )
    )
    assert.ok(
      container.textContent?.includes(
        `Minimum balance: ${minimum}. Current balance: ${balance}.`
      )
    )
  }
})

test('a live FX change refreshes quote, raw charge and minimum without changing the offer', async () => {
  useWalletCurrencyPreferenceStore.getState().setPreference('CNY')
  const container = await mount()
  await act(async () => {
    const currency = useSystemConfigStore.getState().config.currency
    useSystemConfigStore.getState().setConfig({
      currency: { ...currency, cnyPerUsd: 8, cnyPerUsdExact: '8' },
    })
  })
  assert.deepEqual(quoteAmounts(container), [
    '3',
    '0.00001257 CNY',
    '0.00002514 CNY',
  ])
  assert.equal(
    container.querySelector('[data-actual-charge]')?.textContent,
    '0.00001371 CNY'
  )
  assert.ok(
    container.textContent?.includes(
      'Minimum balance: 11.42857143 CNY. Current balance: 11.42856914 CNY.'
    )
  )
})

test('legacy quote units change continuous prices while settled raw charges remain integral', async () => {
  useWalletCurrencyPreferenceStore.getState().setPreference('CREDIT')
  const container = await mount()
  await act(async () => {
    const currency = useSystemConfigStore.getState().config.currency
    useSystemConfigStore
      .getState()
      .setConfig({ currency: { ...currency, quotaPerUnit: 1_000_000 } })
  })
  assert.deepEqual(quoteAmounts(container), ['3', '11 Credits', '22 Credits'])
  assert.equal(
    container.querySelector('[data-actual-charge]')?.textContent,
    '6 Credits'
  )
  assert.ok(container.textContent?.includes('at least 10,000,000 Credits'))
})

test('language changes apply CNY defaults and redraw Credit translations after manual selection', async () => {
  const container = await mount()
  assert.deepEqual(quoteAmounts(container), [
    '3',
    '0.00000157 USD',
    '0.00000314 USD',
  ])
  await act(async () => {
    await i18n.changeLanguage('zhCN')
  })
  assert.deepEqual(quoteAmounts(container), [
    '3',
    '0.000011 CNY',
    '0.000022 CNY',
  ])
  assert.ok(container.textContent?.includes('报价上限'))
  assert.ok(
    container.textContent?.includes('最低余额 10 CNY，当前 9.999998 CNY。')
  )
  await act(async () => {
    useWalletCurrencyPreferenceStore.getState().setPreference('CREDIT')
  })
  assert.deepEqual(quoteAmounts(container), ['3', '5.5 积分', '11 积分'])
  await act(async () => {
    await i18n.changeLanguage('en')
  })
  assert.deepEqual(quoteAmounts(container), ['3', '5.5 Credits', '11 Credits'])
  assert.equal(
    container.querySelector('[data-actual-charge]')?.textContent,
    '6 Credits'
  )
})

test('custom bid input changes denomination without changing the legacy API price', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  const written: string[] = []
  function Bid() {
    const [bidPrice, setBidPrice] = useState('0.000011')
    return (
      <>
        <SmsPriceTierPicker
          offer={{
            ...offer,
            tiers: [
              {
                id: offer.id,
                inventory: 3,
                customer_price_usd: offer.customer_price_usd,
                charge_quota: 6,
              },
            ],
          }}
          selectedTierPrice='0.000011'
          bidEnabled
          bidPrice={bidPrice}
          onTierChange={noop}
          onBidEnabledChange={noop}
          onBidPriceChange={(price) => {
            written.push(price)
            setBidPrice(price)
          }}
        />
        <output data-api-bid>{bidPrice}</output>
      </>
    )
  }
  await act(async () =>
    root?.render(
      <I18nextProvider i18n={i18n}>
        <Bid />
      </I18nextProvider>
    )
  )
  for (const [currency, text] of [
    ['USD', '0.000001571428571428571428571429'],
    ['CNY', '0.000011'],
    ['CREDIT', '5.5'],
  ] as const) {
    await act(async () =>
      useWalletCurrencyPreferenceStore.getState().setPreference(currency)
    )
    const input =
      container.querySelector<HTMLInputElement>('input[type=number]')
    assert.ok(input)
    assert.equal(input.value, text)
    assert.match(
      input.getAttribute('aria-label') ?? '',
      new RegExp(currency === 'CREDIT' ? 'Credits' : currency)
    )
    assert.equal(
      container.querySelector('[data-api-bid]')?.textContent,
      '0.000011'
    )
    assert.deepEqual(written, [], 'changing units must not write a bid')
  }
  const input = container.querySelector<HTMLInputElement>('input[type=number]')
  assert.ok(input)
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(
      dom.HTMLInputElement.prototype,
      'value'
    )?.set
    assert.ok(setter)
    setter.call(input, '6')
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  assert.deepEqual(written, ['0.000012'])
  assert.equal(
    container.querySelector('[data-api-bid]')?.textContent,
    '0.000012'
  )
})
