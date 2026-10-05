/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, beforeEach, test } from 'node:test'

import i18n from '@/i18n/config'
import { useSystemConfigStore } from '@/stores/system-config-store'

import type { PricingModel } from '../types'
import { formatDynamicUnitPrice } from './dynamic-price'
import {
  formatFixedPrice,
  formatGroupPrice,
  formatPrice,
  formatRequestPrice,
} from './price'
import { formatModelPrice } from './price-display'

await i18n.changeLanguage('en')
const savedConfig = useSystemConfigStore.getState().config
beforeEach(() =>
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...savedConfig.currency,
      currencyUnit: 'credit',
      creditsPerUsd: 500_000,
      creditsPerUsdExact: '500000',
      cnyPerUsd: 7,
      cnyPerUsdExact: '7',
      legacyPricingUnitsPerUsd: 14,
    },
  })
)
after(() => useSystemConfigStore.getState().setConfig(savedConfig))
const tokenModel: PricingModel = {
  id: 1,
  model_name: 'test-model',
  quota_type: 0,
  pricing_schema_version: 2,
  pricing_currency: 'USD',
  input_price: 2.5,
  output_price: 15,
  cache_read_price: 0.25,
  model_ratio: 9999,
  completion_ratio: 888,
  cache_ratio: 77,
  enable_groups: ['default', 'cheap'],
  group_ratio: { default: 10, cheap: 0.1 },
}
const requestModel: PricingModel = {
  ...tokenModel,
  quota_type: 1,
  model_price: 5,
}
test('uses canonical USD without a second legacy-unit or FX conversion', () => {
  assert.equal(
    formatPrice(tokenModel, 'input', 'M', 'USD', 'default'),
    '2.5 USD'
  )
  assert.equal(
    formatGroupPrice(
      tokenModel,
      'default',
      'output',
      'M',
      'USD',
      tokenModel.group_ratio ?? {}
    ),
    '150 USD'
  )
  assert.equal(
    formatDynamicUnitPrice(2.5, {
      tokenUnit: 'M',
      displayCurrency: 'USD',
      groupRatioMultiplier: 10,
    }),
    '25 USD'
  )
  assert.equal(
    formatFixedPrice(
      requestModel,
      'default',
      'USD',
      requestModel.group_ratio ?? {}
    ),
    '50 USD'
  )
  assert.equal(formatRequestPrice(requestModel, 'USD', 'default'), '5 USD')
})
test('separates CNY FX, fixed Credit denomination and M/K usage units', () => {
  assert.equal(
    formatPrice(tokenModel, 'input', 'M', 'CNY', 'default'),
    '17.5 CNY'
  )
  assert.equal(
    formatPrice(tokenModel, 'input', 'M', 'CREDIT', 'default'),
    '1,250,000 Credits'
  )
  assert.equal(
    formatPrice(tokenModel, 'input', 'K', 'USD', 'default'),
    '0.0025 USD'
  )
  assert.equal(
    formatPrice(tokenModel, 'cache', 'K', 'CREDIT', 'default'),
    '125 Credits'
  )
  assert.equal(formatPrice(tokenModel, 'input', 'M', 'USD'), '2.5 USD')
  assert.equal(formatModelPrice(0.0000025, 'CREDIT'), '1.25 Credits')
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...savedConfig.currency,
      currencyUnit: 'credit',
      creditsPerUsd: 500_000,
      creditsPerUsdExact: '500000',
      cnyPerUsd: 8,
      cnyPerUsdExact: '8',
    },
  })
  assert.equal(
    formatPrice(tokenModel, 'input', 'M', 'CNY', 'default'),
    '20 CNY'
  )
  assert.equal(
    formatPrice(tokenModel, 'input', 'M', 'CREDIT', 'default'),
    '1,250,000 Credits'
  )
  assert.equal(
    formatPrice(tokenModel, 'input', 'M', 'USD', 'default'),
    '2.5 USD'
  )
})
test('unknown denomination or FX does not invent a one-to-one price', () => {
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...savedConfig.currency,
      currencyUnit: 'unknown',
      creditsPerUsd: 0,
      cnyPerUsd: 0,
      creditsPerUsdExact: '',
      cnyPerUsdExact: '',
    },
  })
  assert.equal(formatPrice(tokenModel, 'input', 'M', 'CREDIT'), '-')
  assert.equal(formatPrice(tokenModel, 'input', 'M', 'CNY'), '-')
  assert.equal(formatPrice(tokenModel, 'input', 'M', 'USD'), '2.5 USD')
})
test('unknown price schemas and absent prices never become zero or USD', () => {
  assert.equal(
    formatPrice(
      { ...tokenModel, pricing_schema_version: undefined },
      'input',
      'M',
      'USD'
    ),
    '-'
  )
  assert.equal(
    formatPrice(
      { ...tokenModel, pricing_currency: 'legacy_pricing_unit' },
      'input',
      'M',
      'USD'
    ),
    '-'
  )
  assert.equal(
    formatPrice({ ...tokenModel, input_price: null }, 'input', 'M', 'USD'),
    '-'
  )
  assert.equal(
    formatRequestPrice({ ...requestModel, model_price: undefined }, 'USD'),
    '-'
  )
})
test('explicit free base prices stay zero independently of group multipliers', () => {
  const free = { ...tokenModel, input_price: 0, group_ratio: { default: 2 } }
  assert.equal(formatPrice(free, 'input', 'M', 'USD', 'default'), '0 USD')
  assert.equal(formatPrice(free, 'input', 'M', 'CNY', 'default'), '0 CNY')
  assert.equal(
    formatPrice(free, 'input', 'M', 'CREDIT', 'default'),
    '0 Credits'
  )
  assert.equal(
    formatPrice(
      { ...tokenModel, input_price: 0 },
      'input',
      'M',
      'USD',
      'default'
    ),
    '0 USD'
  )
})
