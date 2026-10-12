/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, test } from 'node:test'

import { Window } from 'happy-dom'
import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import { useSystemConfigStore } from '@/stores/system-config-store'

import type { PriceDisplayCurrency, PricingModel, TokenUnit } from '../types'
import { ModelCard } from './model-card'

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})
const domWindow = new Window()
const originalConfig = useSystemConfigStore.getState().config
const model: PricingModel = {
  id: 1,
  model_name: 'tiered-cache-read-regression',
  quota_type: 0,
  pricing_schema_version: 2,
  pricing_currency: 'USD',
  // Stale flat prices and billing ratios must not override the expression.
  input_price: 999,
  output_price: 999,
  cache_read_price: 999,
  model_ratio: 9999,
  completion_ratio: 888,
  enable_groups: ['cheap', 'premium'],
  group_ratio: { cheap: 0.13, premium: 2 },
  billing_mode: 'tiered_expr',
  billing_expr:
    'len < 200000 ? tier("short", p*4+c*20+cr*0.4+cc*5+cc1h*8) : tier("long", p*8+c*40+cr*0.8+cc*10+cc1h*16)',
}

beforeEach(() => {
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...originalConfig.currency,
      currencyUnit: 'credit',
      creditsPerUsd: 500_000,
      creditsPerUsdExact: '500000',
      cnyPerUsd: 7,
      cnyPerUsdExact: '7',
      legacyPricingUnitsPerUsd: 1,
    },
  })
})
afterEach(() => {
  useSystemConfigStore.getState().setConfig(originalConfig)
})
after(() => domWindow.close())

function quotes(
  quotedModel: PricingModel = model,
  displayCurrency: PriceDisplayCurrency = 'USD',
  tokenUnit: TokenUnit = 'M',
  selectedGroup = 'all'
) {
  const container = domWindow.document.createElement('div')
  container.innerHTML = renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <ModelCard
        model={quotedModel}
        onClick={() => {}}
        displayCurrency={displayCurrency}
        tokenUnit={tokenUnit}
        selectedGroup={selectedGroup}
      />
    </I18nextProvider>
  )
  const priceBlock = container.querySelector('[class*="border-y"]')
  assert.ok(priceBlock, 'missing card price summary')
  return [...priceBlock.children]
    .slice(1)
    .map((entry) => entry.textContent?.replaceAll(/\s+/g, ' ').trim())
}

for (const { currency, unit, input, output, cache } of [
  {
    currency: 'USD',
    unit: 'M',
    input: '4 USD',
    output: '20 USD',
    cache: '0.4 USD',
  },
  {
    currency: 'USD',
    unit: 'K',
    input: '0.004 USD',
    output: '0.02 USD',
    cache: '0.0004 USD',
  },
  {
    currency: 'CNY',
    unit: 'M',
    input: '28 CNY',
    output: '140 CNY',
    cache: '2.8 CNY',
  },
  {
    currency: 'CNY',
    unit: 'K',
    input: '0.028 CNY',
    output: '0.14 CNY',
    cache: '0.0028 CNY',
  },
  {
    currency: 'CREDIT',
    unit: 'M',
    input: '2,000,000 Credits',
    output: '10,000,000 Credits',
    cache: '200,000 Credits',
  },
  {
    currency: 'CREDIT',
    unit: 'K',
    input: '2,000 Credits',
    output: '10,000 Credits',
    cache: '200 Credits',
  },
] as const) {
  test(`tiered card shows first-tier cache reads in ${currency}/${unit} at base rates`, () => {
    for (const group of ['all', 'cheap', 'premium']) {
      assert.deepEqual(quotes(model, currency, unit, group), [
        `Input ${input}`,
        `Output ${output}`,
        `Cache Read ${cache}`,
      ])
    }
  })
}

test('cache-only tiered cards show a quote even without flat cache pricing', () => {
  const cacheOnly: PricingModel = {
    ...model,
    cache_read_price: null,
    billing_expr: 'len < 200000 ? tier("short", cr*0.4) : tier("long", cr*0.8)',
  }
  for (const group of ['all', 'cheap', 'premium']) {
    assert.deepEqual(quotes(cacheOnly, 'CNY', 'K', group), [
      'Cache Read 0.0028 CNY',
    ])
  }
})

test('mixed duration and cached token quotes retain their distinct units', () => {
  assert.deepEqual(
    quotes(
      {
        ...model,
        billing_expr: 'tier("mixed", p*4+c*20+cr*0.4+cc*5+audio_s*100)',
      },
      'CNY',
      'K'
    ),
    [
      'Input 0.028 CNY/ 1K',
      'Output 0.14 CNY/ 1K',
      'Audio Duration 0.042 CNY/ minute',
      'Cache Read 0.0028 CNY/ 1K',
    ]
  )
})

for (const invalidMetadata of [
  { pricing_schema_version: undefined },
  { pricing_currency: 'CNY' },
]) {
  test(`tiered expressions cannot bypass unavailable canonical quotes: ${JSON.stringify(invalidMetadata)}`, () => {
    assert.deepEqual(quotes({ ...model, ...invalidMetadata }), [
      'Input -',
      'Output -',
      'Cached -',
    ])
  })
}

for (const currency of ['CNY', 'CREDIT'] as const) {
  test(`cache reads remain unavailable when ${currency} conversion is unconfigured`, () => {
    useSystemConfigStore.getState().setConfig({
      currency: {
        ...originalConfig.currency,
        currencyUnit: 'credit',
        creditsPerUsd: 0,
        creditsPerUsdExact: '',
        cnyPerUsd: 0,
        cnyPerUsdExact: '',
      },
    })
    assert.deepEqual(quotes(model, currency), [
      'Input -',
      'Output -',
      'Cache Read -',
    ])
  })
}

test('special expressions do not invent a flat cached quote', () => {
  const summary = quotes({ ...model, billing_expr: 'unknown(p, cr)' }).join(' ')
  assert.match(summary, /Special billing expression/)
  assert.doesNotMatch(summary, /Cached|Cache Read|999 USD/)
})

test('flat token cards keep their existing cached quote', () => {
  assert.deepEqual(
    quotes({
      ...model,
      billing_mode: undefined,
      billing_expr: undefined,
      input_price: 4,
      output_price: 20,
      cache_read_price: 0.4,
    }),
    ['Input 4 USD', 'Output 20 USD', 'Cached 0.4 USD']
  )
})
