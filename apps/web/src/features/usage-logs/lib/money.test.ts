/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { formatQuotaInCurrency, formatUSDInCurrency } from '@/lib/currency'
import { DEFAULT_CURRENCY_CONFIG } from '@/stores/system-config-store'

import type { UsageLog } from '../data/schema'
import { renderAuditContent } from './format'
import {
  buildLogCopyText,
  formatLogPrice,
  formatLogAddonPrice,
  formatLogTokenPrice,
  logExpressionCurrency,
  type LogCurrencyFormatter,
} from './money'

const config = {
  ...DEFAULT_CURRENCY_CONFIG,
  currencyUnit: 'credit' as const,
  creditsPerUsd: 500_000,
  creditsPerUsdExact: '500000',
  cnyPerUsd: 7,
  cnyPerUsdExact: '7',
  // Deliberately unlike the frozen log to catch use of today's old QPU.
  quotaPerUnit: 2_000_000,
}

function formatter(
  overrides: Partial<typeof config> = {}
): LogCurrencyFormatter {
  const currency = 'USD' as const
  const currentConfig = { ...config, ...overrides }
  return {
    currency,
    config: currentConfig,
    formatQuota: (quota, options) =>
      formatQuotaInCurrency(
        quota,
        currency,
        { ...options, locale: 'en' },
        currentConfig
      ),
    formatUSD: (amount, options) =>
      formatUSDInCurrency(
        amount,
        currency,
        { ...options, locale: 'en' },
        currentConfig
      ),
  }
}

describe('log monetary snapshots', () => {
  test('renders audit ledger amounts in USD without mutating stored evidence', () => {
    const other = {
      op: {
        action: 'user.quota_override',
        params: { from: '3500000 Credits', to: '2.000000 USD' },
      },
    }
    const original = JSON.stringify(other)
    const t = (key: string, params?: Record<string, unknown>) =>
      key.replaceAll(/\{\{(\w+)\}\}/g, (_, name: string) =>
        String(params?.[name])
      )
    assert.equal(
      renderAuditContent(other, t, (quota) => formatter().formatQuota(quota)),
      'Overrode user quota from 7 USD to 2.000000 USD'
    )
    assert.equal(JSON.stringify(other), original)
    assert.equal(
      renderAuditContent(
        { op: { action: 'user.quota_add', params: { quota: '7 CNY' } } },
        t,
        (quota) => formatter().formatQuota(quota)
      ),
      'Increased user quota by Not recorded'
    )
  })
  test('converts a frozen legacy absolute price once into USD', () => {
    const other = {
      pricing_unit_credits_per_unit: 500_000,
      pricing_credits_per_usd: 3_500_000,
    }
    const original = JSON.stringify(other)
    assert.equal(formatLogPrice(7, other, formatter()), '1 USD')
    assert.equal(
      formatLogPrice(
        7,
        other,
        formatter({ creditsPerUsd: 1_000_000, cnyPerUsd: 8 })
      ),
      '1 USD'
    )
    assert.equal(JSON.stringify(other), original)
    assert.equal(
      formatLogPrice(
        7,
        { pricing_unit_credits_per_unit: 500_000 },
        formatter()
      ),
      '-'
    )
  })

  test('requires a versioned basis before treating a price as real USD', () => {
    assert.equal(
      formatLogPrice(7, { pricing_currency: 'USD' }, formatter()),
      '-'
    )
    assert.equal(
      formatLogPrice(
        7,
        {
          pricing_schema_version: 2,
          pricing_currency_basis: 'USD',
          pricing_unit_credits_per_unit: 500_000,
          pricing_credits_per_usd: 3_500_000,
        },
        formatter()
      ),
      '7 USD'
    )
    assert.equal(formatLogPrice(7, {}, formatter()), '-')
  })

  test('token ratios use Credits per token, independent of the old QPU', () => {
    const other = { pricing_credits_per_usd: 500_000 }
    assert.equal(formatLogTokenPrice(3.5, 1, other, formatter()), '7 USD')
    assert.equal(formatLogTokenPrice(3.5, 2, other, formatter()), '14 USD')
    assert.match(
      formatLogTokenPrice(0.000_000_01, 1, other, formatter()),
      /^0\.00000002 USD$/
    )
  })

  test('token prices retain their captured scale after the ledger anchor changes', () => {
    const frozen = {
      model_ratio_usd_per_million_multiplier: 1_000_000 / 3_500_000,
      pricing_credits_per_usd: 500_000,
      pricing_unit_credits_per_unit: 2_000_000,
    }
    assert.equal(formatLogTokenPrice(3.5, 1, frozen, formatter()), '1 USD')
    assert.equal(formatLogTokenPrice(3.5, 2, frozen, formatter()), '2 USD')
    assert.equal(
      formatLogTokenPrice(
        3.5,
        1,
        { pricing_credits_per_usd: 3_500_000 },
        formatter()
      ),
      '1 USD'
    )
    assert.equal(formatLogTokenPrice(3.5, 1, {}, formatter()), '-')
    for (const value of [0, -1, Number.NaN, Number.POSITIVE_INFINITY]) {
      assert.equal(
        formatLogTokenPrice(
          3.5,
          1,
          { pricing_credits_per_usd: value },
          formatter()
        ),
        '-'
      )
    }
  })

  test('uses the frozen expression scale and leaves unknown historical prices unavailable', () => {
    assert.deepEqual(logExpressionCurrency({}), {})
    assert.equal(formatLogPrice(7, {}, formatter(), true), '-')
    const frozen = {
      billing_expr_usd_multiplier: 1 / 7,
      pricing_unit_credits_per_unit: 1_000_000,
      pricing_credits_per_usd: 500_000,
    }
    assert.equal(formatLogPrice(7, frozen, formatter(), true), '1 USD')
    assert.equal(
      logExpressionCurrency({
        pricing_unit_credits_per_unit: 500_000,
        pricing_credits_per_usd: 3_500_000,
      }).expressionUsdMultiplier,
      1 / 7
    )
    assert.equal(
      formatLogPrice(
        7,
        { pricing_unit_credits_per_unit: 500_000 },
        formatter(),
        true
      ),
      '-'
    )
    assert.equal(
      formatLogPrice(
        7,
        {
          ...frozen,
          pricing_schema_version: 2,
          billing_expr_currency_basis: 'USD',
        },
        formatter(),
        true
      ),
      '7 USD'
    )
  })

  test('copies raw historical amounts when no captured unit exists and preserves evidence', () => {
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
      formatter(),
      (key) => key
    )
    assert.match(
      usd,
      /Billing Details \(Raw credits\)\nTotal Cost: 3,500,000 Raw credits/
    )
    assert.match(usd, /Final Consumed: 1 Raw credits/)
    assert.match(usd, /Remaining: 3,499,999 Raw credits/)
    assert.ok(usd.endsWith(original))
    assert.doesNotMatch(usd, /Total Cost: .*USD|CNY|Billing Details \(USD\)/)
  })

  test('copies captured charges without revaluing a mixed historical subscription pool', () => {
    const log = { type: 2, quota: 3_500_000 } as UsageLog
    const other = {
      pricing_credits_per_usd: 3_500_000,
      billing_source: 'subscription',
      subscription_consumed: 1_750_000,
      subscription_remain: 3_499_999,
    }
    const copy = buildLogCopyText('', log, other, formatter(), (key) => key)
    assert.match(copy, /Total Cost: 1 USD/)
    assert.match(copy, /Final Consumed: 0\.5 USD/)
    assert.match(copy, /Remaining: 3,499,999 Raw credits/)
    const unavailable = buildLogCopyText(
      '',
      { ...log, quota_money_available: false },
      other,
      formatter(),
      (key) => key
    )
    assert.match(unavailable, /Total Cost: 3,500,000 Raw credits/)
    const refund = buildLogCopyText(
      '',
      { ...log, type: 6 },
      other,
      formatter(),
      (key) => key
    )
    assert.match(refund, /Total Cost: 3,500,000 Raw credits/)
  })

  test('copies absent stored charges as unavailable and preserves real zero and independent subscription remaining', () => {
    const log = {
      type: 2,
      quota: 0,
      quota_money_available: false,
      quota_money_basis: 'invalid_stored_quota',
    } as UsageLog
    const other = { billing_source: 'subscription', subscription_remain: 0 }
    const copy = buildLogCopyText(
      'Original trace',
      log,
      other,
      formatter(),
      (key) => key
    )
    assert.match(copy, /Total Cost: —/)
    assert.match(copy, /Final Consumed: —/)
    assert.match(copy, /Remaining: 0 Raw credits/)
    assert.doesNotMatch(copy, /Total Cost: 0/)
    assert.ok(copy.endsWith('Original trace'))
    const zero = buildLogCopyText(
      '',
      { ...log, quota_money_basis: 'missing_credit_basis' },
      other,
      formatter(),
      (key) => key
    )
    assert.match(zero, /Total Cost: 0 Raw credits/)
    assert.match(zero, /Final Consumed: 0 Raw credits/)
    const auditCopy = buildLogCopyText(
      '',
      { ...log, type: 3 },
      {
        op: { action: 'user.quota_add', params: { quota: '500 Credits' } },
      },
      formatter(),
      (key: string, params?: Record<string, unknown>) =>
        key.replaceAll(/\{\{(\w+)\}\}/g, (_, name: string) =>
          String(params?.[name])
        )
    )
    assert.match(auditCopy, /Increased user quota by 500 Raw credits/)
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
      pricing_credits_per_usd: 3_500_000,
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
      formatter(),
      (key) => key
    )
    assert.match(copy, /Input: 1 USD\/M/)
    assert.match(copy, /Output: 2 USD\/M/)
    assert.doesNotMatch(copy, /0\.2 USD|0\.4 USD/)
    assert.match(copy, /web_search: 2x \(2 USD\/1K\)/)
  })

  test('copied standard token prices use the same frozen scale as the bill details', () => {
    const other = {
      model_ratio: 3.5,
      completion_ratio: 2,
      pricing_credits_per_usd: 3_500_000,
    }
    const copy = buildLogCopyText(
      'Raw recorded evidence',
      { type: 2, quota: 500_000 } as UsageLog,
      other,
      formatter(),
      (key) => key
    )
    assert.match(copy, /Input: 1 USD\/M/)
    assert.match(copy, /Output: 2 USD\/M/)
    assert.ok(copy.endsWith('Raw recorded evidence'))
  })
})

test('audio addon prices use their own frozen unit rather than a separately captured tool scale', () => {
  const other = {
    tool_pricing_unit_credits_per_unit: 500000,
    audio_input_pricing_unit_credits_per_unit: 1000000,
    pricing_credits_per_usd: 3_500_000,
  }
  assert.equal(formatLogAddonPrice(7, other, formatter()), '1 USD')
  assert.equal(formatLogAddonPrice(7, other, formatter(), 'audio'), '2 USD')
  assert.equal(
    formatLogAddonPrice(
      7,
      { ...other, pricing_schema_version: 2, pricing_currency_basis: 'USD' },
      formatter(),
      'audio'
    ),
    '2 USD'
  )
  assert.equal(formatLogAddonPrice(7, {}, formatter(), 'audio'), '-')
})
