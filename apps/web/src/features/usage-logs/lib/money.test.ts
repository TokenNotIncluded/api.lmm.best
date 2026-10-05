/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { formatQuotaInCurrency, formatUSDInCurrency } from '@/lib/currency'
import { DEFAULT_CURRENCY_CONFIG } from '@/stores/system-config-store'

import type { UsageLog } from '../data/schema'
import {
  buildLogCopyText,
  formatLogPrice,
  formatLogTokenPrice,
  logExpressionCurrency,
  type LogCurrencyFormatter,
} from './money'

const config = {
  ...DEFAULT_CURRENCY_CONFIG,
  currencyUnit: 'credit' as const,
  creditsPerUsd: 3_500_000,
  creditsPerUsdExact: '3500000',
  cnyPerUsd: 7,
  cnyPerUsdExact: '7',
  // Deliberately unlike the frozen log to catch use of today's old QPU.
  quotaPerUnit: 2_000_000,
}

function formatter(
  currency: LogCurrencyFormatter['currency']
): LogCurrencyFormatter {
  return {
    currency,
    config,
    formatQuota: (quota, options) =>
      formatQuotaInCurrency(
        quota,
        currency,
        { ...options, locale: 'en' },
        config
      ),
    formatUSD: (amount, options) =>
      formatUSDInCurrency(
        amount,
        currency,
        { ...options, locale: 'en' },
        config
      ),
  }
}

describe('log monetary snapshots', () => {
  test('converts a frozen legacy absolute price once into each display currency', () => {
    const other = { pricing_unit_credits_per_unit: 500_000 }
    assert.equal(formatLogPrice(7, other, formatter('USD')), '1 USD')
    assert.equal(formatLogPrice(7, other, formatter('CNY')), '7 CNY')
    assert.match(formatLogPrice(7, other, formatter('CREDIT')), /^3,500,000 /)
  })

  test('requires a versioned basis before treating a price as real USD', () => {
    assert.equal(
      formatLogPrice(7, { pricing_currency: 'USD' }, formatter('USD')),
      '-'
    )
    assert.equal(
      formatLogPrice(
        7,
        { pricing_schema_version: 2, pricing_currency_basis: 'USD' },
        formatter('CNY')
      ),
      '49 CNY'
    )
    assert.equal(formatLogPrice(7, {}, formatter('USD')), '-')
  })

  test('token ratios use Credits per token, independent of the old QPU', () => {
    assert.equal(formatLogTokenPrice(3.5, 1, formatter('USD')), '1 USD')
    assert.equal(formatLogTokenPrice(3.5, 2, formatter('CNY')), '14 CNY')
    assert.match(
      formatLogTokenPrice(0.000_000_01, 1, formatter('CREDIT')),
      /^0\.01 /
    )
  })

  test('uses the frozen expression scale and leaves unknown historical prices unavailable', () => {
    assert.deepEqual(logExpressionCurrency({}, 3_500_000), {})
    assert.equal(formatLogPrice(7, {}, formatter('USD'), true), '-')
    const frozen = { billing_expr_usd_multiplier: 1 / 7 }
    assert.equal(formatLogPrice(7, frozen, formatter('USD'), true), '1 USD')
    assert.equal(formatLogPrice(7, frozen, formatter('CNY'), true), '7 CNY')
    assert.equal(
      logExpressionCurrency(
        { pricing_unit_credits_per_unit: 500_000 },
        3_500_000
      ).expressionUsdMultiplier,
      1 / 7
    )
  })

  test('copies the displayed bill with its unit while preserving original content verbatim', () => {
    const log = { type: 2, quota: 3_500_000 } as UsageLog
    const other = {
      billing_source: 'subscription',
      subscription_consumed: 1,
      subscription_remain: 3_499_999,
    }
    const original = 'Historical $7 (Platform)\nOriginal trace'
    const usd = buildLogCopyText(
      original,
      log,
      other,
      formatter('USD'),
      (key) => key
    )
    assert.match(usd, /Billing Details \(USD\)\nTotal Cost: 1 USD/)
    assert.match(usd, /Final Consumed: 0\.00000029 USD/)
    assert.ok(usd.endsWith(original))
    const cny = buildLogCopyText(
      original,
      log,
      other,
      formatter('CNY'),
      (key) => key
    )
    assert.match(cny, /Total Cost: 7 CNY/)
    assert.match(cny, /Final Consumed: 0\.000002 CNY/)
    const credit = buildLogCopyText(
      original,
      log,
      other,
      formatter('CREDIT'),
      (key) => key
    )
    assert.match(credit, /Total Cost: 3,500,000 /)
    assert.match(credit, /Final Consumed: 1 /)
  })

  test('copies matched expression prices and each tool surcharge with its own frozen unit', () => {
    const other = {
      billing_mode: 'tiered_expr',
      expr_b64: Buffer.from('tier("matched", p * 7 + c * 14)').toString(
        'base64'
      ),
      matched_tier: 'matched',
      pricing_schema_version: 2,
      pricing_unit_credits_per_unit: 500_000,
      billing_expr_usd_multiplier: 1 / 7,
      model_ratio: 0.7,
      completion_ratio: 2,
      tool_surcharges: [
        {
          name: 'web_search',
          count: 2,
          price: 7,
          pricing_unit_credits_per_unit: 1_000_000,
        },
      ],
    }
    const copy = buildLogCopyText(
      '',
      { type: 2, quota: 3_500_000 } as UsageLog,
      other,
      formatter('USD'),
      (key) => key
    )
    assert.match(copy, /Input: 1 USD\/M/)
    assert.match(copy, /Output: 2 USD\/M/)
    assert.doesNotMatch(copy, /0\.2 USD|0\.4 USD/)
    assert.match(copy, /web_search: 2x \(2 USD\/1K\)/)
  })
})
