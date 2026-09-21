/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import i18n from '@/i18n/config'

import type { PricingModel } from '../types'
import { formatDynamicUnitPrice } from './dynamic-price'
import {
  formatFixedPrice,
  formatGroupPrice,
  formatPrice,
  formatRequestPrice,
} from './price'

await i18n.changeLanguage('en')

const tokenModel: PricingModel = {
  id: 1,
  model_name: 'test-model',
  quota_type: 0,
  model_ratio: 1.25,
  completion_ratio: 6,
  cache_ratio: 0.1,
  enable_groups: ['default', 'cheap'],
  group_ratio: { default: 10, cheap: 0.1 },
}
const requestModel: PricingModel = {
  ...tokenModel,
  quota_type: 1,
  model_price: 5,
}

test('converts platform prices using platform units per USD, not fiat FX twice', () => {
  // 1 CNY buys 2 platform units, 1 USD = 7 CNY, so 14 units cost 1 USD.
  assert.equal(
    formatPrice(tokenModel, 'input', 'M', true, 14, 7, 'default'),
    '1.7857 USD'
  )
  assert.equal(
    formatGroupPrice(
      tokenModel,
      'default',
      'output',
      'M',
      true,
      14,
      7,
      tokenModel.group_ratio ?? {}
    ),
    '10.7143 USD'
  )
  assert.equal(
    formatDynamicUnitPrice(2.5, {
      tokenUnit: 'M',
      showRechargePrice: true,
      priceRate: 14,
      usdExchangeRate: 7,
      groupRatioMultiplier: 10,
    }),
    '1.7857 USD'
  )
  assert.equal(
    formatFixedPrice(
      requestModel,
      'default',
      true,
      14,
      7,
      requestModel.group_ratio ?? {}
    ),
    '3.5714 USD'
  )
  assert.equal(
    formatRequestPrice(requestModel, true, 14, 7, 'default'),
    '3.5714 USD'
  )
})

test('platform prices do not change with fiat FX and retain the selected group', () => {
  assert.equal(
    formatPrice(tokenModel, 'input', 'M', false, 14, 7, 'default'),
    '$25 (Platform)'
  )
  assert.equal(
    formatPrice(tokenModel, 'input', 'M', false, 14, 7),
    '$0.25 (Platform)'
  )
  assert.equal(
    formatPrice(tokenModel, 'input', 'K', false, 14, 7, 'default'),
    '$0.025 (Platform)'
  )
  assert.equal(
    formatPrice(tokenModel, 'cache', 'K', true, 14, 7, 'default'),
    '0.000179 USD'
  )
})

test('invalid recharge rates never produce a fabricated payable price', () => {
  for (const rate of [0, -1, Number.NaN, Number.POSITIVE_INFINITY]) {
    assert.equal(formatPrice(tokenModel, 'input', 'M', true, rate, 7), '-')
    assert.equal(
      formatDynamicUnitPrice(2.5, {
        tokenUnit: 'M',
        showRechargePrice: true,
        priceRate: rate,
      }),
      '-'
    )
  }
})

test('free groups remain zero in platform and real USD display', () => {
  const free = { ...tokenModel, group_ratio: { default: 0 } }
  assert.equal(
    formatPrice(free, 'input', 'M', false, 7, 7, 'default'),
    '$0 (Platform)'
  )
  assert.equal(formatPrice(free, 'input', 'M', true, 7, 7, 'default'), '0 USD')
})
