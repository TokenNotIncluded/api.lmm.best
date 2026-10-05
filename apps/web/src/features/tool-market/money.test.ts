/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { displayAmountToQuota, quotaToDisplayInput } from '@/lib/currency'
import { DEFAULT_CURRENCY_CONFIG } from '@/stores/system-config-store'

import { marketNetQuota, marketQuota } from './money'

const config = {
  ...DEFAULT_CURRENCY_CONFIG,
  currencyUnit: 'credit' as const,
  creditsPerUsd: 3500000,
  creditsPerUsdExact: '3500000',
  cnyPerUsd: 7,
  cnyPerUsdExact: '7',
}

test('market inputs preserve native raw Credits across all display currencies', () => {
  for (const currency of ['CREDIT', 'CNY', 'USD'] as const) {
    const convert = (input: string) =>
      displayAmountToQuota(input, currency, config)
    for (const raw of [0, 1, 11, 96338, 1470000, Number.MAX_SAFE_INTEGER]) {
      assert.equal(
        marketQuota(quotaToDisplayInput(raw, currency, config), convert),
        raw,
        `${currency} ${raw}`
      )
    }
  }
  assert.equal(
    marketQuota('7', (input) => displayAmountToQuota(input, 'CNY', config)),
    3500000
  )
  assert.equal(
    marketQuota('1', (input) => displayAmountToQuota(input, 'USD', config)),
    3500000
  )
  assert.equal(marketQuota('1', Number), 1)
})

test('market inputs reject fractional raw Credits, sub-credit fiat, unsafe integers and numeric coercion', () => {
  for (const amount of [
    '0.5',
    '-1',
    '1e3',
    'Infinity',
    'NaN',
    '',
    '0x10',
    '9007199254740992',
    '9007199254740991.1',
  ]) {
    assert.throws(
      () => marketQuota(amount, Number),
      /Invalid amount/,
      amount || 'empty'
    )
  }
  assert.throws(
    () =>
      marketQuota('0.00000000001', (input) =>
        displayAmountToQuota(input, 'USD', config)
      ),
    /Invalid amount/
  )
  assert.throws(() => marketQuota('1', () => Number.NaN), /Invalid amount/)
})

test('author earnings use the configured fee and the exact integer settlement rounding', () => {
  assert.equal(marketNetQuota(500000, 1000), 450000)
  assert.equal(marketNetQuota(500000, 2500), 375000)
  assert.equal(marketNetQuota(500000, 0), 500000)
  assert.equal(marketNetQuota(500000, 10000), 0)
  assert.equal(marketNetQuota(0, 1000), 0)
  assert.equal(marketNetQuota(1, 1000), 1)
  assert.equal(marketNetQuota(11, 1000), 10)
  assert.equal(marketNetQuota(Number.MAX_SAFE_INTEGER, 1000), 8106479329266892)
})
