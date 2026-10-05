/*
Copyright (C) 2026 LIghtJUNction
*/
import type { CurrencyFormatOptions } from '@/lib/currency'

import type { UsageLog } from '../data/schema'
import type { LogOtherData } from '../types'
import { getTieredBillingSummary } from './format'

export interface LogCurrencyFormatter {
  currency: 'CREDIT' | 'CNY' | 'USD'
  config: { creditsPerUsd?: number }
  formatQuota: (quota: number, options?: CurrencyFormatOptions) => string
  formatUSD: (amount: number, options?: CurrencyFormatOptions) => string
}

const PRICE_OPTIONS = { digitsLarge: 4, digitsSmall: 8, abbreviate: false }

function positiveNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0
}

/** A plain historical USD label is not evidence of a true USD price. */
export function formatLogPrice(
  amount: number,
  other: LogOtherData,
  currency: LogCurrencyFormatter,
  expression = false
): string {
  if (expression) {
    const metadata = logExpressionCurrency(other, currency.config.creditsPerUsd)
    return metadata.expressionUsdMultiplier == null
      ? '-'
      : currency.formatUSD(
          amount * metadata.expressionUsdMultiplier,
          PRICE_OPTIONS
        )
  }
  if (
    other.pricing_schema_version === 2 &&
    other.pricing_currency_basis === 'USD'
  ) {
    return currency.formatUSD(amount, PRICE_OPTIONS)
  }
  if (positiveNumber(other.pricing_unit_credits_per_unit)) {
    return currency.formatUSD(
      (amount * other.pricing_unit_credits_per_unit) /
        Number(currency.config.creditsPerUsd),
      PRICE_OPTIONS
    )
  }
  return '-'
}

/** Separate audio and tool charges can capture different legacy settlement units. */
export function formatLogAddonPrice(
  amount: number,
  other: LogOtherData,
  currency: LogCurrencyFormatter,
  kind: 'tool' | 'audio' = 'tool'
): string {
  return formatLogPrice(
    amount,
    {
      ...other,
      pricing_unit_credits_per_unit:
        kind === 'audio'
          ? (other.audio_input_pricing_unit_credits_per_unit ??
            other.tool_pricing_unit_credits_per_unit)
          : other.tool_pricing_unit_credits_per_unit,
    },
    currency
  )
}

/** Historical expressions without a frozen scale cannot safely expose prices. */
export function logExpressionCurrency(
  other: LogOtherData,
  creditsPerUSD: number | undefined
): {
  expressionCurrencyBasis?: 'USD' | 'legacy_pricing_unit'
  expressionUsdMultiplier?: number
} {
  if (
    other.pricing_schema_version === 2 &&
    other.billing_expr_currency_basis === 'USD'
  ) {
    return { expressionCurrencyBasis: 'USD', expressionUsdMultiplier: 1 }
  }
  if (positiveNumber(other.billing_expr_usd_multiplier)) {
    return {
      expressionCurrencyBasis: 'legacy_pricing_unit',
      expressionUsdMultiplier: other.billing_expr_usd_multiplier,
    }
  }
  if (
    positiveNumber(other.pricing_unit_credits_per_unit) &&
    positiveNumber(creditsPerUSD)
  ) {
    return {
      expressionCurrencyBasis: 'legacy_pricing_unit',
      expressionUsdMultiplier:
        other.pricing_unit_credits_per_unit / creditsPerUSD,
    }
  }
  return {}
}

/** Ratios are calibrated Credits per token; they do not depend on legacy QPU. */
export function formatLogTokenPrice(
  modelRatio: number,
  multiplier: number,
  currency: LogCurrencyFormatter
): string {
  return currency.formatUSD(
    (modelRatio * 1_000_000 * multiplier) /
      Number(currency.config.creditsPerUsd),
    PRICE_OPTIONS
  )
}

/** Copy the currently displayed bill and preserve the original text as evidence. */
export function buildLogCopyText(
  original: string,
  log: UsageLog,
  other: LogOtherData | null,
  currency: LogCurrencyFormatter,
  t: (key: string) => string
): string {
  if (![1, 2, 6].includes(log.type)) return original
  const format = (quota: number) => currency.formatQuota(quota, PRICE_OPTIONS)
  const rows = [
    `${t('Billing Details')} (${currency.currency})`,
    `${t('Total Cost')}: ${format(log.quota)}`,
    `${t('Credits')}: ${log.quota.toLocaleString('en', { maximumFractionDigits: 0 })}`,
    `${t('Billing Source')}: ${t(other?.billing_source === 'subscription' ? 'Subscription' : 'Wallet')}`,
  ]
  if (other?.billing_mode === 'tiered_expr') {
    const tier = getTieredBillingSummary(other)
    for (const entry of tier?.priceEntries ?? []) {
      rows.push(
        `${t(entry.shortLabel)}: ${formatLogPrice(entry.price, other, currency, true)}/M`
      )
    }
  } else if (other?.model_price != null && other.model_price >= 0) {
    rows.push(
      `${t('Model Price')}: ${formatLogPrice(other.model_price, other, currency)}`
    )
  } else if (other?.model_ratio != null) {
    rows.push(
      `${t('Input')}: ${formatLogTokenPrice(other.model_ratio, 1, currency)}/M`
    )
    if (other.completion_ratio != null) {
      rows.push(
        `${t('Output')}: ${formatLogTokenPrice(other.model_ratio, other.completion_ratio, currency)}/M`
      )
    }
  }
  for (const item of other?.tool_surcharges ?? []) {
    rows.push(
      `${item.name}: ${item.count}x (${formatLogPrice(
        item.price,
        {
          pricing_schema_version: other?.pricing_schema_version,
          pricing_currency_basis: item.price_currency_basis,
          pricing_unit_credits_per_unit: item.pricing_unit_credits_per_unit,
        },
        currency
      )}/1K)`
    )
  }
  if (other?.billing_source === 'subscription') {
    rows.push(
      `${t('Final Consumed')}: ${format(other.subscription_consumed ?? log.quota)}`
    )
    if (other.subscription_remain != null) {
      rows.push(`${t('Remaining')}: ${format(other.subscription_remain)}`)
    }
  }
  if (other?.fee_quota != null) {
    rows.push(`${t('Fee')}: ${format(other.fee_quota)}`)
  }
  if (original) rows.push('', `${t('Original log content')}:`, original)
  return rows.join('\n')
}
