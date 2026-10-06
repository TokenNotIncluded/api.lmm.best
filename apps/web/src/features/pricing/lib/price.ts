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
/*
Copyright (C) 2026 LIghtJUNction
*/
import { QUOTA_TYPE_VALUES, TOKEN_UNIT_DIVISORS } from '../constants'
import type {
  PricingModel,
  TokenUnit,
  PriceType,
  PriceDisplayCurrency,
} from '../types'
import { getConfiguredGroupRatio, getDisplayGroupRatio } from './model-helpers'
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
import { formatModelPrice } from './price-display'

// ----------------------------------------------------------------------------
// Price Calculation Utilities
// ----------------------------------------------------------------------------

/**
 * Strip trailing zeros from formatted price string while preserving currency symbols
 */
export function stripTrailingZeros(formatted: string): string {
  // Match currency symbol at start, number, and potential 'k' suffix
  const match = formatted.match(/^([^\d-]*)([-\d,]+\.?\d*)(k?)$/)
  if (!match) return formatted

  const [, symbol, number, suffix] = match

  // Remove commas for processing
  const cleanNumber = number.replaceAll(',', '')

  // Convert to number and back to remove trailing zeros
  const parsed = Number.parseFloat(cleanNumber)
  if (Number.isNaN(parsed)) return formatted

  // Convert to string, which automatically removes trailing zeros
  let result = parsed.toString()

  // If the result is in scientific notation, format it properly
  if (result.includes('e')) {
    result = parsed.toFixed(20).replace(/\.?0+$/, '')
  }

  return `${symbol}${result}${suffix}`
}

/** Only the versioned API's real-USD fields are valid price inputs. */
export function hasCanonicalPricing(model: PricingModel): boolean {
  return model.pricing_schema_version === 2 && model.pricing_currency === 'USD'
}

export function getTokenPriceUSD(model: PricingModel, type: PriceType): number {
  if (!hasCanonicalPricing(model)) return Number.NaN
  const fields: Record<PriceType, keyof PricingModel> = {
    input: 'input_price',
    output: 'output_price',
    cache: 'cache_read_price',
    create_cache: 'cache_write_price',
    image: 'image_price',
    audio_input: 'audio_input_price',
    audio_output: 'audio_output_price',
  }
  const value = model[fields[type]]
  return typeof value === 'number' && Number.isFinite(value) && value >= 0
    ? value
    : Number.NaN
}

/**
 * Format token-based price for display
 */
export function formatPrice(
  model: PricingModel,
  type: PriceType,
  tokenUnit: TokenUnit,
  displayCurrency: PriceDisplayCurrency = 'USD',
  selectedGroup?: string
): string {
  if (model.quota_type === QUOTA_TYPE_VALUES.REQUEST) {
    return '-'
  }

  const displayGroupRatio = getDisplayGroupRatio(model, selectedGroup)

  const amountUSD = getTokenPriceUSD(model, type) * displayGroupRatio

  const price = amountUSD / TOKEN_UNIT_DIVISORS[tokenUnit]
  return formatModelPrice(price, displayCurrency, {
    digitsLarge: 4,
    digitsSmall: 6,
    abbreviate: false,
  })
}

/**
 * Format price for a specific group (token-based)
 */
export function formatGroupPrice(
  model: PricingModel,
  group: string,
  type: PriceType,
  tokenUnit: TokenUnit,
  displayCurrency: PriceDisplayCurrency = 'USD',
  groupRatio: Record<string, number>
): string {
  if (model.quota_type === QUOTA_TYPE_VALUES.REQUEST) {
    return '-'
  }

  const ratio = getConfiguredGroupRatio(groupRatio, group)
  const amountUSD = getTokenPriceUSD(model, type) * ratio

  const price = amountUSD / TOKEN_UNIT_DIVISORS[tokenUnit]
  return formatModelPrice(price, displayCurrency, {
    digitsLarge: 4,
    digitsSmall: 6,
    abbreviate: false,
  })
}

/**
 * Format fixed price for pay-per-request models (with specific group)
 */
export function formatFixedPrice(
  model: PricingModel,
  group: string,
  displayCurrency: PriceDisplayCurrency = 'USD',
  groupRatio: Record<string, number>
): string {
  if (model.quota_type !== QUOTA_TYPE_VALUES.REQUEST) {
    return '-'
  }

  const ratio = getConfiguredGroupRatio(groupRatio, group)
  const amountUSD =
    hasCanonicalPricing(model) && typeof model.model_price === 'number'
      ? model.model_price * ratio
      : Number.NaN

  return formatModelPrice(amountUSD, displayCurrency, {
    digitsLarge: 4,
    digitsSmall: 4,
    abbreviate: false,
  })
}

/**
 * Format the canonical base price for pay-per-request models.
 */
export function formatRequestPrice(
  model: PricingModel,
  displayCurrency: PriceDisplayCurrency = 'USD',
  selectedGroup?: string
): string {
  if (model.quota_type !== QUOTA_TYPE_VALUES.REQUEST) {
    return '-'
  }

  const displayGroupRatio = getDisplayGroupRatio(model, selectedGroup)

  const amountUSD =
    hasCanonicalPricing(model) && typeof model.model_price === 'number'
      ? model.model_price * displayGroupRatio
      : Number.NaN

  return formatModelPrice(amountUSD, displayCurrency, {
    digitsLarge: 4,
    digitsSmall: 4,
    abbreviate: false,
  })
}
