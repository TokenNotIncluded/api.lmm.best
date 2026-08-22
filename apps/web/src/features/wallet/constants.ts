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
// ============================================================================
// Wallet Constants
// ============================================================================

/**
 * Default preset amount multipliers
 * Used to generate quick select amounts based on minimum topup
 */
export const DEFAULT_PRESET_MULTIPLIERS = [1, 5, 10, 30, 50, 100, 300, 500]

/**
 * Payment method types
 */
export const PAYMENT_TYPES = {
  ALIPAY: 'alipay',
  WECHAT: 'wxpay',
  STRIPE: 'stripe',
  CREEM: 'creem',
  WAFFO: 'waffo',
  WAFFO_PANCAKE: 'waffo_pancake',
} as const

/**
 * Payment icon colors. The actual palette lives in forge-tokens.css so icons
 * follow the same source of truth as the rest of the editorial surface.
 */
export const PAYMENT_ICON_COLORS = {
  [PAYMENT_TYPES.ALIPAY]: 'var(--forge-payment-alipay)',
  [PAYMENT_TYPES.WECHAT]: 'var(--forge-payment-wechat)',
  [PAYMENT_TYPES.STRIPE]: 'var(--forge-payment-stripe)',
  [PAYMENT_TYPES.CREEM]: 'var(--forge-payment-creem)',
  [PAYMENT_TYPES.WAFFO]: 'var(--forge-payment-waffo)',
  [PAYMENT_TYPES.WAFFO_PANCAKE]: 'var(--forge-payment-waffo-pancake)',
} as const

/**
 * Default discount rate (no discount)
 */
export const DEFAULT_DISCOUNT_RATE = 1.0

/**
 * Default minimum topup amount
 */
export const DEFAULT_MIN_TOPUP = 1
