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
import {
  formatFiatCurrencyAmount,
  formatPlatformAmount,
  getPlatformCurrencyLabel,
} from '@/lib/currency'

import { DEFAULT_DISCOUNT_RATE } from '../constants'

// ============================================================================
// Wallet-specific Formatting Functions
// ============================================================================

/** Format a Creem fiat price with its ISO currency code. */
export function formatCreemPrice(
  price: number,
  currency: 'USD' | 'EUR'
): string {
  return formatFiatCurrencyAmount(price, currency, {
    abbreviate: false,
    digitsLarge: 2,
    digitsSmall: 2,
  })
}

/**
 * Format large quota numbers with K/M suffix
 */
export function formatQuotaShort(quota: number): string {
  if (quota >= 1000000) {
    return `${(quota / 1000000).toFixed(1)}M`
  }
  if (quota >= 1000) {
    return `${(quota / 1000).toFixed(1)}K`
  }
  return quota.toString()
}

/**
 * Format currency amount that is already in local currency.
 * This is used for payment amounts that have been calculated via priceRatio.
 */
export function formatCurrency(amount: number | string): string {
  const numeric =
    typeof amount === 'number' ? amount : Number.parseFloat(String(amount))
  if (!Number.isFinite(numeric)) return '-'

  return new Intl.NumberFormat(undefined, {
    minimumFractionDigits: 0,
    maximumFractionDigits: Math.abs(numeric) >= 1 ? 2 : 4,
  }).format(numeric)
}

/** Payment amounts use an explicit gateway ISO code; USD is the fallback. */
export function getPaymentCurrencyLabel(): string {
  return 'USD'
}

/** API top-up credits use the virtual platform currency label. */
export function getCreditCurrencyLabel(): string {
  return getPlatformCurrencyLabel()
}

/**
 * Format an API credit amount as virtual platform currency.
 *
 * The amount is USD-denominated for accounting, but it is not fiat. Keep the
 * explicit `(Platform)` marker whenever it is shown to a user.
 */
export function formatPlatformCreditBalance(
  amount: number,
  platformLabel?: string
): string {
  return formatPlatformAmount(
    amount,
    {
      abbreviate: false,
      digitsLarge: 2,
      digitsSmall: 4,
    },
    platformLabel
  )
}

/** Format a visible platform credit amount (never fiat USD). */
export function formatCreditBalance(
  amount: number,
  platformLabel?: string
): string {
  return formatPlatformCreditBalance(amount, platformLabel)
}

/** Format the fiat amount that will actually be charged. */
export function formatPaymentAmount(amount: number, currency?: string): string {
  if (currency) {
    return formatFiatCurrencyAmount(amount, currency, {
      abbreviate: false,
      digitsLarge: 2,
      digitsSmall: 2,
    })
  }

  // Unknown/legacy gateways default to fiat USD rather than inheriting the
  // platform display currency. Configured gateways pass their unit explicitly.
  return formatFiatCurrencyAmount(amount, 'USD', {
    abbreviate: false,
    digitsLarge: 2,
    digitsSmall: 2,
  })
}

/** Format a system-USD credit value without repeating its unit label. */
export function formatCreditValue(amount: number): string {
  return new Intl.NumberFormat(undefined, {
    maximumFractionDigits: Math.abs(amount) >= 1 ? 2 : 4,
  }).format(amount)
}

/** Format an actual fiat USD payment with its ISO code. */
export function formatPaymentMoney(amount: number): string {
  return formatFiatCurrencyAmount(amount, 'USD', {
    abbreviate: false,
    digitsLarge: 2,
    digitsSmall: 2,
  })
}

/**
 * Get discount label for display (e.g., "20% OFF")
 */
export function getDiscountLabel(discount: number): string {
  if (discount >= DEFAULT_DISCOUNT_RATE) {
    return ''
  }
  const off = Math.round((1 - discount) * 100)
  return `${off}% OFF`
}

/**
 * Calculate pricing details for a preset amount
 */
export function calculatePresetPricing(
  presetValue: number,
  priceRatio: number,
  discount: number,
  usdExchangeRate: number = 1
) {
  const originalPrice = presetValue * priceRatio
  const actualPrice = originalPrice * discount
  const savedAmount = originalPrice - actualPrice
  const hasDiscount = discount < 1.0
  const displayValue = presetValue * usdExchangeRate

  return {
    displayValue,
    originalPrice,
    actualPrice,
    savedAmount,
    hasDiscount,
  }
}
