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
import type { PaymentMethodData } from './payment-method-dialog'

const SETTLEMENT_UNIT_PATTERN = /^[A-Za-z0-9._-]{1,16}$/
const POSITIVE_DECIMAL_PATTERN = /^[0-9]+(?:\.[0-9]+)?$/
const NON_NEGATIVE_INTEGER_PATTERN = /^(?:0|[1-9][0-9]*)$/
const NON_NEGATIVE_DECIMAL_PATTERN = /^[0-9]+(?:\.[0-9]+)?$/
const AUDIENCE_MODES = new Set(['legacy', 'all', 'include', 'exclude'])
const AUDIENCE_MATCH_MODES = new Set(['any', 'all'])
const OAUTH_PROVIDERS = new Set([
  'linuxdo',
  'linux.do',
  'github',
  'discord',
  'oidc',
  'wechat',
  'telegram',
])
const AUDIENCE_CONDITION_FIELDS = [
  'audience_email_contains',
  'audience_oauth_provider',
  'audience_linuxdo_score_min',
  'audience_linuxdo_score_max',
  'audience_user_group',
  'audience_role',
] as const

export function isValidPaymentMethodData(
  item: unknown
): item is PaymentMethodData {
  if (
    typeof item !== 'object' ||
    item === null ||
    !('name' in item) ||
    !('type' in item) ||
    typeof item.name !== 'string' ||
    typeof item.type !== 'string' ||
    ('icon' in item && typeof item.icon !== 'string') ||
    ('enabled' in item &&
      item.enabled !== 'true' &&
      item.enabled !== 'false') ||
    ('description' in item && typeof item.description !== 'string') ||
    ('min_topup' in item &&
      (typeof item.min_topup !== 'string' ||
        !NON_NEGATIVE_DECIMAL_PATTERN.test(item.min_topup) ||
        !Number.isFinite(Number(item.min_topup)))) ||
    ('max_topup' in item &&
      (typeof item.max_topup !== 'string' ||
        !POSITIVE_DECIMAL_PATTERN.test(item.max_topup) ||
        Number(item.max_topup) <= 0 ||
        !Number.isFinite(Number(item.max_topup)))) ||
    ('unlock_after_days' in item &&
      (typeof item.unlock_after_days !== 'string' ||
        !NON_NEGATIVE_INTEGER_PATTERN.test(item.unlock_after_days) ||
        !Number.isSafeInteger(Number(item.unlock_after_days)))) ||
    ('topup_ratio' in item &&
      (typeof item.topup_ratio !== 'string' ||
        !POSITIVE_DECIMAL_PATTERN.test(item.topup_ratio) ||
        Number(item.topup_ratio) <= 0)) ||
    ('color' in item &&
      (typeof item.color !== 'string' ||
        (item.color !== '' && !/^#[0-9a-fA-F]{6}$/.test(item.color))))
  ) {
    return false
  }

  const record = item as Record<string, unknown>

  if (
    typeof record.min_topup === 'string' &&
    typeof record.max_topup === 'string' &&
    NON_NEGATIVE_DECIMAL_PATTERN.test(record.min_topup) &&
    Number(record.min_topup) > Number(record.max_topup)
  ) {
    return false
  }

  if (
    !('audience_mode' in record) &&
    (AUDIENCE_CONDITION_FIELDS.some((field) => field in record) ||
      'audience_match' in record)
  ) {
    return false
  }

  if ('audience_mode' in record) {
    if (
      typeof record.audience_mode !== 'string' ||
      !AUDIENCE_MODES.has(record.audience_mode)
    ) {
      return false
    }
    if (
      'audience_match' in record &&
      (typeof record.audience_match !== 'string' ||
        !AUDIENCE_MATCH_MODES.has(record.audience_match))
    ) {
      return false
    }
    for (const field of [
      'audience_email_contains',
      'audience_oauth_provider',
      'audience_user_group',
      'audience_role',
    ]) {
      if (field in record && typeof record[field] !== 'string') return false
    }
    if (
      'audience_oauth_provider' in record &&
      (!record.audience_oauth_provider ||
        !OAUTH_PROVIDERS.has(String(record.audience_oauth_provider)))
    ) {
      return false
    }
    if (
      'audience_role' in record &&
      record.audience_role !== 'none' &&
      !new Set(['common', 'admin', 'root']).has(String(record.audience_role))
    ) {
      return false
    }
    for (const field of [
      'audience_linuxdo_score_min',
      'audience_linuxdo_score_max',
    ]) {
      if (
        field in record &&
        (typeof record[field] !== 'string' ||
          !NON_NEGATIVE_DECIMAL_PATTERN.test(record[field]) ||
          !Number.isFinite(Number(record[field])))
      ) {
        return false
      }
    }

    const scoreMin = Number(record.audience_linuxdo_score_min)
    const scoreMax = Number(record.audience_linuxdo_score_max)
    if (
      'audience_linuxdo_score_min' in record &&
      'audience_linuxdo_score_max' in record &&
      scoreMin > scoreMax
    ) {
      return false
    }
    const hasCondition = AUDIENCE_CONDITION_FIELDS.some((field) => {
      if (!(field in record) || typeof record[field] !== 'string') return false
      const value = record[field].trim()
      return value !== '' && !(field === 'audience_role' && value === 'none')
    })
    const filtersUsers =
      record.audience_mode === 'include' || record.audience_mode === 'exclude'
    if (filtersUsers !== hasCondition) return false
    if (!filtersUsers && 'audience_match' in record) return false
  }

  const hasSettlementCurrency = 'settlement_currency' in record
  const hasSettlementRate = 'settlement_units_per_usd' in record
  const hasPlatformRate = 'platform_units_per_usd' in record
  const hasPreferredPricing =
    hasSettlementCurrency || hasSettlementRate || hasPlatformRate
  const hasSettlementUnit = 'settlement_unit' in record
  const hasExplicitDirectRate = 'settlement_units_per_platform_unit' in record
  const hasUnitPrice = 'unit_price' in record
  const hasLegacyPricing =
    hasSettlementUnit || hasExplicitDirectRate || hasUnitPrice

  if (hasPreferredPricing) {
    if (hasLegacyPricing || !hasSettlementCurrency || !hasSettlementRate) {
      return false
    }
    if (
      typeof record.settlement_currency !== 'string' ||
      !/^[A-Za-z]{3}$/.test(record.settlement_currency) ||
      typeof record.settlement_units_per_usd !== 'string' ||
      !POSITIVE_DECIMAL_PATTERN.test(record.settlement_units_per_usd) ||
      Number(record.settlement_units_per_usd) <= 0
    ) {
      return false
    }
    if (
      hasPlatformRate &&
      (typeof record.platform_units_per_usd !== 'string' ||
        !POSITIVE_DECIMAL_PATTERN.test(record.platform_units_per_usd) ||
        Number(record.platform_units_per_usd) <= 0)
    ) {
      return false
    }
    return true
  }

  if (!hasLegacyPricing) return true
  if (!hasSettlementUnit || (!hasExplicitDirectRate && !hasUnitPrice)) {
    return false
  }
  if (
    typeof record.settlement_unit !== 'string' ||
    !SETTLEMENT_UNIT_PATTERN.test(record.settlement_unit)
  ) {
    return false
  }
  const explicitDirectRate = record.settlement_units_per_platform_unit
  const unitPrice = record.unit_price
  if (
    hasExplicitDirectRate &&
    (typeof explicitDirectRate !== 'string' ||
      !POSITIVE_DECIMAL_PATTERN.test(explicitDirectRate) ||
      Number(explicitDirectRate) <= 0)
  ) {
    return false
  }
  if (
    hasUnitPrice &&
    (typeof unitPrice !== 'string' ||
      !POSITIVE_DECIMAL_PATTERN.test(unitPrice) ||
      Number(unitPrice) <= 0)
  ) {
    return false
  }
  return !(
    typeof explicitDirectRate === 'string' &&
    typeof unitPrice === 'string' &&
    explicitDirectRate !== unitPrice
  )
}
