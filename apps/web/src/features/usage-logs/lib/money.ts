/*
Copyright (C) 2026 LIghtJUNction
*/
import type { CurrencyFormatOptions } from '@/lib/currency'
import {
  formatHistoricalRawQuota,
  formatHistoricalQuotaFallback,
  historicalLogQuotaUSD,
} from '@/lib/historical-quota-money'

import type { UsageLog } from '../data/schema'
import type { LogOtherData } from '../types'
import { getTieredBillingSummary, renderAuditContent } from './format'

export interface LogCurrencyFormatter {
  currency: 'USD'
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
    const metadata = logExpressionCurrency(other)
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
  if (
    positiveNumber(other.pricing_unit_credits_per_unit) &&
    positiveNumber(other.pricing_credits_per_usd)
  ) {
    return currency.formatUSD(
      (amount * other.pricing_unit_credits_per_unit) /
        other.pricing_credits_per_usd,
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
  const creditsPerUnit =
    kind === 'audio'
      ? (other.audio_input_pricing_unit_credits_per_unit ??
        other.tool_pricing_unit_credits_per_unit)
      : other.tool_pricing_unit_credits_per_unit
  return formatLogPrice(
    amount,
    {
      ...other,
      pricing_currency_basis: positiveNumber(creditsPerUnit)
        ? 'legacy_pricing_unit'
        : other.pricing_currency_basis,
      pricing_unit_credits_per_unit: creditsPerUnit,
    },
    currency
  )
}

/** Historical expressions without a frozen scale cannot safely expose prices. */
export function logExpressionCurrency(other: LogOtherData): {
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
    positiveNumber(other.pricing_credits_per_usd)
  ) {
    return {
      expressionCurrencyBasis: 'legacy_pricing_unit',
      expressionUsdMultiplier:
        other.pricing_unit_credits_per_unit / other.pricing_credits_per_usd,
    }
  }
  return {}
}

/** Ratios need the request's captured Credit-to-USD scale, never today's anchor. */
export function formatLogTokenPrice(
  modelRatio: number,
  multiplier: number,
  other: LogOtherData,
  currency: LogCurrencyFormatter
): string {
  const scale = positiveNumber(other.model_ratio_usd_per_million_multiplier)
    ? other.model_ratio_usd_per_million_multiplier
    : positiveNumber(other.pricing_credits_per_usd)
      ? 1_000_000 / other.pricing_credits_per_usd
      : null
  if (scale == null) return '-'
  return currency.formatUSD(modelRatio * scale * multiplier, PRICE_OPTIONS)
}

/** Partial or invalid native metadata must never fall back to a legacy total. */
export function hasNetConsumptionMetadata(other: LogOtherData | null): boolean {
  return (
    other?.billing_basis !== undefined ||
    other?.gross_settled_credits !== undefined ||
    other?.refunded_credits !== undefined ||
    other?.net_consumption_credits !== undefined
  )
}

export function logNetConsumptionAmounts(
  log: UsageLog,
  other: LogOtherData | null
): { gross: number; refunded: number; net: number } | null {
  const gross = other?.gross_settled_credits
  const refunded = other?.refunded_credits
  const net = other?.net_consumption_credits
  const safeAmount = (value: unknown): value is number =>
    typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
  if (
    log.type !== 2 ||
    other?.billing_basis !== 'net_consumption' ||
    other.request_state !== 'settled' ||
    other.usage_status !== 'settled' ||
    log.quota_money_basis === 'invalid_stored_quota' ||
    !safeAmount(gross) ||
    !safeAmount(refunded) ||
    !safeAmount(net) ||
    !safeAmount(log.quota) ||
    gross < refunded ||
    gross - refunded !== net ||
    net !== log.quota
  ) {
    return null
  }
  return { gross, refunded, net }
}

/** Shared rows keep the details dialog and copied bill on the same basis. */
export function logCostRows(
  log: UsageLog,
  other: LogOtherData | null,
  format: (quota: number) => string,
  t: (key: string) => string
): Array<{ label: string; value: string }> {
  if (!hasNetConsumptionMetadata(other)) {
    return [{ label: t('Total Cost'), value: format(log.quota) }]
  }
  const amounts = logNetConsumptionAmounts(log, other)
  return [
    {
      label: t('Gross settlement'),
      value: amounts ? format(amounts.gross) : '—',
    },
    {
      label: t('Recorded refunds'),
      value: amounts ? format(amounts.refunded) : '—',
    },
    {
      label: t('Net consumption'),
      value: amounts ? format(amounts.net) : '—',
    },
  ]
}

/** Copy the currently displayed bill and preserve the original text as evidence. */
export function buildLogCopyText(
  original: string,
  log: UsageLog,
  other: LogOtherData | null,
  currency: LogCurrencyFormatter,
  t: (key: string) => string
): string {
  const formatRaw = (quota: number) =>
    formatHistoricalRawQuota(quota, t('Raw credits'))
  const format = (quota: number) => {
    const usd = historicalLogQuotaUSD({
      quota: log.quota,
      other,
      type: log.type,
      projection: log,
      consumedQuota: quota,
    })
    return usd === null
      ? formatHistoricalQuotaFallback(quota, log, t('Raw credits'))
      : currency.formatUSD(usd, PRICE_OPTIONS)
  }
  if (log.type === 3) {
    const audit = renderAuditContent(other, t, formatRaw)
    return audit
      ? `${audit}\n\n${t('Original log content')}:\n${original}`
      : original
  }
  if (![1, 2, 6].includes(log.type)) return original
  const moneyAvailable =
    historicalLogQuotaUSD({
      quota: log.quota,
      other,
      type: log.type,
      projection: log,
    }) !== null
  const rows = [
    `${t('Billing Details')} (${moneyAvailable ? currency.currency : t('Raw credits')})`,
    ...logCostRows(log, other, format, t).map(
      (row) => `${row.label}: ${row.value}`
    ),
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
      `${t('Input')}: ${formatLogTokenPrice(other.model_ratio, 1, other, currency)}/M`
    )
    if (other.completion_ratio != null) {
      rows.push(
        `${t('Output')}: ${formatLogTokenPrice(other.model_ratio, other.completion_ratio, other, currency)}/M`
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
          pricing_credits_per_usd: other?.pricing_credits_per_usd,
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
      rows.push(`${t('Remaining')}: ${formatRaw(other.subscription_remain)}`)
    }
  }
  if (other?.fee_quota != null) {
    rows.push(`${t('Fee')}: ${format(other.fee_quota)}`)
  }
  if (original) rows.push('', `${t('Original log content')}:`, original)
  return rows.join('\n')
}
