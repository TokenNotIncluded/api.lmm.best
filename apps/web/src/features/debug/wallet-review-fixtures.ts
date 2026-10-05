/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import type { TopupInfo } from '@/features/wallet/types'

// Synthetic local review data. These constants never configure production money.
export const DEBUG_CURRENCY_STATUS = {
  currency_unit: 'credit',
  credits_per_usd: 3500000,
  cny_per_usd: 7,
  quota_per_unit: 500000,
  legacy_pricing_units_per_usd: 7,
  usd_exchange_rate: 7,
} as const

export const DEBUG_WALLET_TOPUP_INFO = {
  amount_unit: 'LEGACY',
  credit_metadata_available: true,
  credit_metadata_version: 1,
  credit_amount_options: [5000000, 25000000, 50000000, 100000000],
  credit_discount: {},
  credit_min_topup: 500000,
  stripe_credit_min_topup: 500000,
  waffo_credit_min_topup: 0,
  pancake_credit_min_topup: 0,
  stripe_credit_max_topup: 5000000000,
  waffo_credit_max_topup: null,
  pancake_credit_max_topup: null,
  legacy_amount_unit: 'LEGACY',
  legacy_amount_options: [10, 50, 100, 200],
  legacy_discount: {},
  enable_online_topup: true,
  enable_stripe_topup: false,
  enable_creem_topup: false,
  enable_waffo_topup: false,
  enable_waffo_pancake_topup: false,
  waffo_pancake_currency: 'USD',
  pay_methods: [
    {
      name: 'Alipay · local preview',
      type: 'alipay',
      min_topup: 1,
      min_topup_unit: 'USD',
      min_topup_credit: 3500000,
      legacy_min_topup: '7',
      max_topup: '100',
      max_topup_credit: 350000000,
      max_topup_amount: '700',
      max_topup_amount_unit: 'LEGACY',
      legacy_max_topup_amount: '700',
      settlement_currency: 'CNY',
      platform_units_per_usd: 7,
      settlement_units_per_usd: 7,
      settlement_units_per_platform_unit: 1,
    },
  ],
  amount_options: [10, 50, 100, 200],
  min_topup: 1,
  stripe_min_topup: 1,
  discount: {},
  topup_group_ratio: 1,
  payment_compliance_confirmed: true,
  developer_access_granted: true,
  payment_available: true,
} satisfies TopupInfo & {
  // Required in the latest version-1 catalog; kept explicit for older type snapshots.
  stripe_credit_max_topup: number | null
  waffo_credit_max_topup: number | null
  pancake_credit_max_topup: number | null
}

/** Read-only decimal projection, without creating an order or applying provider cents. */
export function debugCreditQuoteAmount(
  credits: number,
  currency: 'USD' | 'CNY'
): string {
  const scale = 10n ** 30n
  const numerator =
    BigInt(credits) *
    BigInt(currency === 'CNY' ? DEBUG_CURRENCY_STATUS.cny_per_usd : 1)
  const denominator = BigInt(DEBUG_CURRENCY_STATUS.credits_per_usd)
  const digits = ((numerator * scale) / denominator)
    .toString()
    .padStart(31, '0')
  return `${digits.slice(0, -30)}.${digits.slice(-30)}`
    .replace(/0+$/, '')
    .replace(/\.$/, '')
}
