/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { DEFAULT_CURRENCY_CONFIG } from '@/stores/system-config-store'

import { heroSmsInputToPrice, heroSmsPriceToInput } from './price-input'

const config = {
  ...DEFAULT_CURRENCY_CONFIG,
  currencyUnit: 'credit' as const,
  quotaPerUnit: 500000,
  creditsPerUsd: 500000,
  creditsPerUsdExact: '500000',
  cnyPerUsd: 8,
  cnyPerUsdExact: '8',
}

test('legacy bids round-trip across actual fiat and fractional quoted Credits', () => {
  for (const unit of ['USD', 'CNY', 'CREDIT'] as const) {
    for (const quote of ['0.000001', '0.000011', '1', '2.94', '1000000']) {
      const input = heroSmsPriceToInput(quote, unit, config)
      assert.notEqual(input, '')
      assert.equal(
        heroSmsInputToPrice(input, unit, config),
        quote,
        `${unit}: ${quote}`
      )
    }
  }
  assert.equal(heroSmsPriceToInput('0.000011', 'CREDIT', config), '5.5')
  assert.equal(heroSmsInputToPrice('5.5', 'CREDIT', config), '0.000011')
  assert.equal(heroSmsInputToPrice('1', 'USD', config), '1')
  assert.equal(heroSmsInputToPrice('8', 'CNY', config), '1')
})

test('maximum bids never increase when quantized to the existing six-decimal API', () => {
  assert.equal(heroSmsInputToPrice('0.000010999999', 'USD', config), '0.00001')
  assert.equal(heroSmsInputToPrice('5.4999999', 'CREDIT', config), '0.00001')
  for (const input of ['', '-1', '1e3', '0x10', 'Infinity', 'NaN']) {
    assert.equal(heroSmsInputToPrice(input, 'USD', config), '')
  }
  assert.equal(
    heroSmsPriceToInput('1', 'USD', { ...config, creditsPerUsdExact: '' }),
    ''
  )
  assert.equal(
    heroSmsPriceToInput('1', 'USD', { ...config, currencyUnit: 'unknown' }),
    ''
  )
})
