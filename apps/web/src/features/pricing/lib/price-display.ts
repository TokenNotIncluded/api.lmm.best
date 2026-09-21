/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import {
  formatFiatCurrencyAmount,
  formatPlatformAmount,
  type CurrencyFormatOptions,
} from '@/lib/currency'
import { platformUnitsToUsd } from '@/lib/payment-pricing'

/**
 * /api/status.price is platform units per real USD (CNY/USD × units/CNY).
 * Convert once by dividing; the fiat FX rate is already included in that
 * denominator. This is a base-rate estimate, not a checkout quote.
 */
export function formatModelPrice(
  platformAmount: number,
  showRechargePrice: boolean,
  platformUnitsPerUSD: number,
  options: CurrencyFormatOptions = {}
): string {
  if (!Number.isFinite(platformAmount) || platformAmount < 0) return '-'
  const formatOptions = {
    digitsLarge: 4,
    digitsSmall: 6,
    abbreviate: false,
    ...options,
  }
  if (!showRechargePrice) {
    return formatPlatformAmount(platformAmount, formatOptions)
  }
  if (!Number.isFinite(platformUnitsPerUSD) || platformUnitsPerUSD <= 0) {
    return '-'
  }
  return formatFiatCurrencyAmount(
    platformUnitsToUsd(platformAmount, platformUnitsPerUSD),
    'USD',
    formatOptions
  )
}
