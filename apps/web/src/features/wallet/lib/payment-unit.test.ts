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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  calculateSettlementAmount,
  createLegacyUsdSettlementMetadata,
  formatPaymentSettlementRate,
  formatSettlementAmount,
  getPaymentMaxTopup,
  getPaymentTopupRatio,
  getPaymentSettlementMetadata,
  getPaymentSettlementUnit,
} from './payment-unit'

describe('payment settlement units', () => {
  test('uses the two-rate settlement contract for real USD and CNY examples', () => {
    const platformAmount = 6.8
    const usdMetadata = getPaymentSettlementMetadata({
      name: 'USD card',
      type: 'card',
      settlement_currency: 'USD',
      platform_units_per_usd: '6.8',
      settlement_units_per_usd: '1',
    })
    const cnyMetadata = getPaymentSettlementMetadata({
      name: 'CNY card',
      type: 'card',
      settlement_currency: 'CNY',
      platform_units_per_usd: '6.8',
      settlement_units_per_usd: '6.8',
    })

    assert.ok(usdMetadata)
    assert.ok(cnyMetadata)
    assert.equal(calculateSettlementAmount(platformAmount, usdMetadata), 1)
    assert.equal(formatSettlementAmount(1, usdMetadata.currencyCode), '1 USD')
    assert.equal(calculateSettlementAmount(platformAmount, cnyMetadata), 6.8)
    assert.equal(
      formatSettlementAmount(6.8, cnyMetadata.currencyCode),
      '6.8 CNY'
    )
  })

  test('accepts catalog aliases and explicit direct legacy pricing', () => {
    const fxMetadata = getPaymentSettlementMetadata({
      name: 'CNY catalog method',
      type: 'epay',
      settlement_unit: 'CNY',
      platform_units_per_usd: '6.8',
      settlement_units_per_usd: '6.8',
    })
    const directMetadata = getPaymentSettlementMetadata({
      name: 'LDC direct method',
      type: 'epay',
      settlement_unit: 'LDC',
      settlement_units_per_platform_unit: '10',
    })

    assert.ok(fxMetadata)
    assert.equal(calculateSettlementAmount(6.8, fxMetadata), 6.8)
    assert.ok(directMetadata)
    assert.equal(directMetadata.source, 'legacy-unit-price')
    assert.equal(calculateSettlementAmount(2, directMetadata), 20)
  })

  test('isolates the no-metadata fallback as real USD settlement', () => {
    const fallback = createLegacyUsdSettlementMetadata(0.14)

    assert.equal(fallback.currencyCode, 'USD')
    assert.equal(fallback.source, 'legacy-usd-price-ratio')
    assert.ok(
      Math.abs(calculateSettlementAmount(6.8, fallback) - 0.952) < 1e-12
    )
  })

  test('does not fall through to legacy fields when preferred metadata is incomplete', () => {
    assert.equal(
      getPaymentSettlementMetadata({
        name: 'Broken preferred contract',
        type: 'card',
        settlement_currency: 'CNY',
        platform_units_per_usd: '6.8',
        settlement_unit: 'LDC',
        unit_price: '10',
      }),
      null
    )
  })

  test('normalizes the per-payment maximum credited USD', () => {
    assert.equal(
      getPaymentMaxTopup({
        name: 'LINUX DO Credit',
        type: 'epay',
        max_topup: '20.5',
      }),
      20.5
    )
    assert.equal(
      getPaymentMaxTopup({ name: 'Card', type: 'stripe', max_topup: 100 }),
      100
    )
    for (const maxTopup of ['0', '-1', '1e2', ' 20 ', 'NaN']) {
      assert.equal(
        getPaymentMaxTopup({
          name: 'Invalid method',
          type: 'epay',
          max_topup: maxTopup,
        }),
        null
      )
    }
  })

  test('normalizes a Linux.do LDC price per credited USD', () => {
    assert.deepEqual(
      getPaymentSettlementUnit({
        name: 'LINUX DO Credit',
        settlement_unit: 'LDC',
        type: 'epay',
        unit_price: '10',
      }),
      { label: 'LDC', unitPrice: 10 }
    )
    assert.equal(formatSettlementAmount(0.14, 'LDC'), '0.14 LDC')
  })

  test('does not render a custom currency for incomplete or invalid metadata', () => {
    assert.equal(
      getPaymentSettlementUnit({ name: 'Linux', type: 'epay' }),
      null
    )
    assert.equal(
      getPaymentSettlementUnit({
        name: 'Linux',
        settlement_unit: 'LDC',
        type: 'epay',
        unit_price: 'not-a-number',
      }),
      null
    )
    assert.equal(
      getPaymentSettlementUnit({
        name: 'Linux',
        settlement_unit: 'LDC',
        type: 'epay',
        unit_price: '1e3',
      }),
      null
    )
  })

  test('rejects unsafe settlement-unit metadata from the server', () => {
    for (const settlementUnit of [
      ' LDC ',
      'LD C',
      'LDC\u0000',
      'LDC!',
      'A'.repeat(17),
    ]) {
      assert.equal(
        getPaymentSettlementUnit({
          name: 'Linux',
          settlement_unit: settlementUnit,
          type: 'epay',
          unit_price: '10',
        }),
        null
      )
    }
  })

  test('rejects whitespace around a server-provided gateway price', () => {
    assert.equal(
      getPaymentSettlementUnit({
        name: 'Linux',
        settlement_unit: 'LDC',
        type: 'epay',
        unit_price: ' 10 ',
      }),
      null
    )
  })

  test('ignores generic settlement metadata on dedicated payment flows', () => {
    for (const type of ['stripe', 'waffo', 'waffo_pancake']) {
      const method = {
        name: type,
        settlement_unit: 'LDC',
        type,
        unit_price: '10',
      }

      assert.equal(getPaymentSettlementUnit(method), null)
      assert.equal(formatPaymentSettlementRate(method), null)
      assert.equal(getPaymentTopupRatio({ ...method, topup_ratio: '0.5' }), 1)
    }
  })

  test('preserves exact configured decimal rates in the wallet label', () => {
    assert.equal(
      formatPaymentSettlementRate({
        name: 'Tiny-rate gateway',
        settlement_unit: 'TOKEN',
        type: 'epay',
        unit_price: '0.000000000001',
      }),
      '0.000000000001 TOKEN / USD'
    )
    assert.equal(
      formatPaymentSettlementRate({
        name: 'Precise gateway',
        settlement_unit: 'TOKEN',
        type: 'custom-provider-method',
        unit_price: '1.2345',
      }),
      '1.2345 TOKEN / USD'
    )
  })

  test('normalizes a per-method top-up multiplier with a backward-compatible default', () => {
    assert.equal(
      getPaymentTopupRatio({
        name: 'LINUX DO Credit',
        topup_ratio: '0.5',
        type: 'epay',
      }),
      0.5
    )
    assert.equal(
      getPaymentTopupRatio({ name: 'Legacy method', type: 'alipay' }),
      1
    )

    for (const topupRatio of ['0', '-1', '1e2', ' 2 ', 'NaN']) {
      assert.equal(
        getPaymentTopupRatio({
          name: 'Invalid method',
          topup_ratio: topupRatio,
          type: 'epay',
        }),
        1
      )
    }
  })
})
