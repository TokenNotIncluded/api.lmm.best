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
  creditsPerUsd: 3_500_000,
  creditsPerUsdExact: '3500000',
  cnyPerUsd: 7,
  cnyPerUsdExact: '7',
  // Deliberately unlike the frozen log to catch use of today's old QPU.
  quotaPerUnit: 2_000_000,
}

function formatter(): LogCurrencyFormatter {
  const currency = 'USD' as const
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
      'Overrode user quota from 1 USD to 2.000000 USD'
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
    const other = { pricing_unit_credits_per_unit: 500_000 }
    assert.equal(formatLogPrice(7, other, formatter()), '1 USD')
  })

  test('requires a versioned basis before treating a price as real USD', () => {
    assert.equal(
      formatLogPrice(7, { pricing_currency: 'USD' }, formatter()),
      '-'
    )
    assert.equal(
      formatLogPrice(
        7,
        { pricing_schema_version: 2, pricing_currency_basis: 'USD' },
        formatter()
      ),
      '7 USD'
    )
    assert.equal(formatLogPrice(7, {}, formatter()), '-')
  })

  test('token ratios use Credits per token, independent of the old QPU', () => {
    assert.equal(formatLogTokenPrice(3.5, 1, formatter()), '1 USD')
    assert.equal(formatLogTokenPrice(3.5, 2, formatter()), '2 USD')
    assert.match(
      formatLogTokenPrice(0.000_000_01, 1, formatter()),
      /^0\.00000000286 USD$/
    )
  })

  test('uses the frozen expression scale and leaves unknown historical prices unavailable', () => {
    assert.deepEqual(logExpressionCurrency({}, 3_500_000), {})
    assert.equal(formatLogPrice(7, {}, formatter(), true), '-')
    const frozen = { billing_expr_usd_multiplier: 1 / 7 }
    assert.equal(formatLogPrice(7, frozen, formatter(), true), '1 USD')
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
      formatter(),
      (key) => key
    )
    assert.match(usd, /Billing Details \(USD\)\nTotal Cost: 1 USD/)
    assert.match(usd, /Final Consumed: 0\.00000029 USD/)
    assert.ok(usd.endsWith(original))
    assert.doesNotMatch(usd, /Credits:|CNY|Billing Details \(CREDIT\)/)
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
      formatter(),
      (key) => key
    )
    assert.match(copy, /Input: 1 USD\/M/)
    assert.match(copy, /Output: 2 USD\/M/)
    assert.doesNotMatch(copy, /0\.2 USD|0\.4 USD/)
    assert.match(copy, /web_search: 2x \(2 USD\/1K\)/)
  })
})

test('audio addon prices use their own frozen unit rather than a separately captured tool scale', () => {
  const other = {
    tool_pricing_unit_credits_per_unit: 500000,
    audio_input_pricing_unit_credits_per_unit: 1000000,
  }
  assert.equal(formatLogAddonPrice(7, other, formatter()), '1 USD')
  assert.equal(formatLogAddonPrice(7, other, formatter(), 'audio'), '2 USD')
  assert.equal(formatLogAddonPrice(7, other, formatter(), 'audio'), '2 USD')
  assert.match(formatLogAddonPrice(7, other, formatter(), 'audio'), /^2 USD$/)
  assert.equal(formatLogAddonPrice(7, {}, formatter(), 'audio'), '-')
})
