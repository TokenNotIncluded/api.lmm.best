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

For commercial licensing, please contact support@quantumnous.com
*/
/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { DEFAULT_CURRENCY_CONFIG } from '@/stores/system-config-store'

import {
  usesDedicatedPaymentPricing,
  platformUnitsToUsd,
  legacyMinimumToUsdInput,
  usdToLegacyMinimum,
  legacySettlementRatePerUsd,
  getLegacyGatewaySettlementUnit,
} from './payment-pricing'

test('wallet fallback uses the same USD bridge as model estimates', () => {
  assert.equal(platformUnitsToUsd(14, 14), 1)
  assert.equal(platformUnitsToUsd(1, 14), 1 / 14)
  for (const rate of [0, -1, Number.NaN, Number.POSITIVE_INFINITY]) {
    assert.ok(Number.isNaN(platformUnitsToUsd(10, rate)))
  }
})

test('built-in gateways cannot expose custom settlement pricing', () => {
  for (const type of ['stripe', 'waffo', 'waffo_pancake', 'alipay', 'wxpay']) {
    assert.equal(usesDedicatedPaymentPricing(type), true, type)
  }
  assert.equal(usesDedicatedPaymentPricing('custom_gateway'), false)
})

const fixedConfig = {
  ...DEFAULT_CURRENCY_CONFIG,
  currencyUnit: 'credit' as const,
  creditsPerUsd: 500000,
  creditsPerUsdExact: '500000',
  quotaPerUnit: 500000,
  cnyPerUsd: 6.8,
}

test('integer gateway minimums show real USD without rounding new edits', () => {
  assert.equal(legacyMinimumToUsdInput(68, fixedConfig), '68')
  assert.equal(legacyMinimumToUsdInput(1, fixedConfig), '1')
  assert.equal(usdToLegacyMinimum('10', fixedConfig), 10)
  assert.equal(usdToLegacyMinimum('5', fixedConfig), 5)
  assert.equal(usdToLegacyMinimum('0', fixedConfig), 0)
  for (const value of [
    '1.5',
    '0.999999999999999999999999999999',
    '',
    '-1',
    'Infinity',
    '10000000000000000000000000',
  ]) {
    assert.ok(Number.isNaN(usdToLegacyMinimum(value, fixedConfig)), value)
  }
  assert.equal(legacyMinimumToUsdInput(1.5, fixedConfig), '')
})

test('gateway settings never derive fixed denomination from live FX or top-up pricing', () => {
  const changed = {
    ...fixedConfig,
    cnyPerUsd: 100,
    usdExchangeRate: 999,
    legacyPricingUnitsPerUsd: 999,
  }
  assert.equal(usdToLegacyMinimum('10', changed), 10)
  assert.equal(legacyMinimumToUsdInput(68, changed), '68')
  assert.equal(legacySettlementRatePerUsd('1.25', changed), '1.25')
  for (const creditsPerUsd of [0, -1, Number.NaN, Infinity]) {
    const unknown = { ...fixedConfig, creditsPerUsd }
    assert.equal(legacyMinimumToUsdInput(1, unknown), '')
    assert.ok(Number.isNaN(usdToLegacyMinimum('1', unknown)))
    assert.equal(legacySettlementRatePerUsd('1', unknown), '')
  }
  assert.equal(
    legacyMinimumToUsdInput(1, { ...fixedConfig, currencyUnit: 'unknown' }),
    ''
  )
})

test('native direct gateway prices preserve decimal rates through the fixed batch bridge', () => {
  assert.equal(
    legacySettlementRatePerUsd('0.0000001', fixedConfig),
    '0.0000001'
  )
  assert.equal(legacySettlementRatePerUsd('1', fixedConfig), '1')
  for (const value of ['0', '-1', 'NaN', '', 'Infinity']) {
    assert.equal(legacySettlementRatePerUsd(value, fixedConfig), '')
  }
})

test('legacy gateway unit defaults follow the protocol and require explicit non-fiat units', () => {
  assert.equal(
    getLegacyGatewaySettlementUnit('epay', 'Ordinary gateway'),
    'CNY'
  )
  assert.equal(
    getLegacyGatewaySettlementUnit('custom', 'Custom gateway'),
    'CNY'
  )
  assert.equal(
    getLegacyGatewaySettlementUnit('epay', 'Ordinary gateway', 'USD'),
    'USD'
  )
  for (const name of ['LDC gateway', 'LinuxDO credits', 'Linux DO']) {
    assert.equal(getLegacyGatewaySettlementUnit('epay', name), '')
    assert.equal(getLegacyGatewaySettlementUnit('epay', name, 'LDC'), 'LDC')
  }
  assert.equal(getLegacyGatewaySettlementUnit('alipay', 'Alipay', 'USD'), 'CNY')
  assert.equal(getLegacyGatewaySettlementUnit('wxpay', 'WeChat', 'USD'), 'CNY')
})
