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
  credit_unit_schema_version: 2,
  quota_unit: 'LEDGER_QUOTA',
  legacy_credit_unit: 'LEDGER_QUOTA',
  public_credit_unit: 'CREDIT',
  ledger_quota_per_usd: 3500000,
  ledger_quota_per_usd_exact: '3500000',
  public_credits_per_usd: 100000,
  public_credits_per_usd_exact: '100000',
  credits_per_usd: 3500000,
  cny_per_usd: 7,
  quota_per_unit: 500000,
  legacy_pricing_units_per_usd: 7,
  usd_exchange_rate: 7,
} as const

const rawWalletCatalog = {
  amount_unit: 'LEGACY',
  credit_metadata_available: true,
  credit_metadata_version: 1,
  credit_amount_options: [5000000, 25000000, 50000000, 100000000],
  credit_discount: { 50000000: 0.9 },
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

function publicCredits(raw: number): string {
  const precision = 10n ** 64n
  const numerator =
    BigInt(raw) *
    BigInt(DEBUG_CURRENCY_STATUS.public_credits_per_usd) *
    precision
  const denominator = BigInt(DEBUG_CURRENCY_STATUS.ledger_quota_per_usd)
  const projected = (numerator + denominator / 2n) / denominator
  const digits = projected.toString().padStart(65, '0')
  const tail = digits.slice(-64).replace(/0+$/, '')
  return digits.slice(0, -64) + (tail ? `.${tail}` : '')
}
export function debugCreditGrant(raw: number) {
  return {
    ...DEBUG_CURRENCY_STATUS,
    credit_amount: raw,
    credited_quota: raw,
    credit_amount_unit: 'LEDGER_QUOTA',
    public_credit_metadata_version: 2,
    public_credit_amount_unit: 'CREDIT',
    public_credit_amount: publicCredits(raw),
  }
}
export const DEBUG_WALLET_TOPUP_INFO = {
  ...rawWalletCatalog,
  ...DEBUG_CURRENCY_STATUS,
  public_credit_metadata_version: 2,
  public_credit_amount_unit: 'CREDIT',
  ledger_quota_amount_options: rawWalletCatalog.credit_amount_options,
  public_credit_amount_options:
    rawWalletCatalog.credit_amount_options.map(publicCredits),
  ledger_quota_discount: rawWalletCatalog.credit_discount,
  public_credit_discount: { [publicCredits(50000000)]: 0.9 },
  ledger_quota_min_topup: rawWalletCatalog.credit_min_topup,
  public_credit_min_topup: publicCredits(rawWalletCatalog.credit_min_topup),
  ...Object.fromEntries(
    ['stripe_', 'waffo_', 'pancake_'].flatMap((prefix) => {
      const catalog = rawWalletCatalog as Record<string, unknown>
      const min = Number(catalog[`${prefix}credit_min_topup`])
      const max = catalog[`${prefix}credit_max_topup`] as number | null
      return [
        [`${prefix}ledger_quota_min_topup`, min],
        [`${prefix}public_credit_min_topup`, publicCredits(min)],
        [`${prefix}ledger_quota_max_topup`, max],
        [
          `${prefix}public_credit_max_topup`,
          max === null ? null : publicCredits(max),
        ],
      ]
    })
  ),
  pay_methods: rawWalletCatalog.pay_methods.map((method) => ({
    ...method,
    credit_amount_unit: 'LEDGER_QUOTA',
    min_topup_credit: String(method.min_topup_credit),
    max_topup_credit: String(method.max_topup_credit),
    min_topup_ledger_quota: String(method.min_topup_credit),
    max_topup_ledger_quota: String(method.max_topup_credit),
    min_topup_public_credit: publicCredits(method.min_topup_credit),
    max_topup_public_credit: publicCredits(method.max_topup_credit),
  })),
} satisfies TopupInfo

/** Only this explicit synthetic coupon is accepted in the existing review controls. */
export const DEBUG_DISCOUNT_CODE = 'PREVIEW20'
export function debugSettlementQuote(
  raw: number,
  currency: 'USD' | 'CNY',
  code = ''
) {
  const numerator =
    BigInt(raw) *
    BigInt(currency === 'CNY' ? DEBUG_CURRENCY_STATUS.cny_per_usd : 1) *
    100n
  const denominator = BigInt(DEBUG_CURRENCY_STATUS.ledger_quota_per_usd)
  const round = (numerator: bigint, denominator: bigint) =>
    (numerator + denominator / 2n) / denominator
  const original = round(numerator, denominator)
  const preset = raw === 50000000 ? 90n : 100n
  const coupon = code === DEBUG_DISCOUNT_CODE ? 80n : 100n
  const paid = round(numerator * preset * coupon, denominator * 10000n)
  const fixed2 = (value: bigint) =>
    `${value / 100n}.${(value % 100n).toString().padStart(2, '0')}`
  if (original === 0n || paid === 0n) {
    return { data: debugCreditQuoteAmount(raw, currency) }
  }
  const data = fixed2(paid)
  const savings = original - paid
  return {
    data,
    ...(savings > 0n
      ? {
          settlement_quote: {
            schema_version: 1,
            currency,
            original_amount: fixed2(original),
            paid_amount: data,
            savings_amount: fixed2(savings),
            discount_percent: fixed2(round(savings * 10000n, original)),
            basis: 'amount_preset_and_code',
          },
        }
      : {}),
  }
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
