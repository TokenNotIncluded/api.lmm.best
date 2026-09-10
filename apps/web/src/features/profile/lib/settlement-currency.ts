/*
Copyright (C) 2026 LIghtJUNction
*/
import type {
  FiatSettlementCurrency,
  SettlementCurrencyPreference,
} from '../types'

/** Settings can arrive as JSON or an auth-store record, including legacy data. */
export function parseSettlementSettings(
  setting: unknown
): Record<string, unknown> {
  let value = setting
  if (typeof value === 'string') {
    try {
      value = JSON.parse(value) as unknown
    } catch {
      return {}
    }
  }
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {}
}

export function settlementCurrencyPreference(
  value: unknown
): SettlementCurrencyPreference {
  // Unsupported legacy preferences follow the same explicit automatic policy.
  const normalized = typeof value === 'string' ? value.trim().toUpperCase() : ''
  return normalized === 'CNY' || normalized === 'USD' ? normalized : ''
}

export function resolveSettlementCurrency(
  setting: unknown,
  browserLocale?: string
): FiatSettlementCurrency {
  const settings = parseSettlementSettings(setting)
  const preference = settlementCurrencyPreference(settings.settlement_currency)
  if (preference) return preference

  const storedLanguage =
    typeof settings.language === 'string' ? settings.language.trim() : ''
  const language = (storedLanguage || browserLocale || '')
    .split(',')[0]
    .split(';')[0]
    .trim()
    .toLowerCase()
  return language === 'zh' ||
    language.startsWith('zh-') ||
    language.startsWith('zh_')
    ? 'CNY'
    : 'USD'
}
