/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
// Synthetic wire fixtures only. These do not configure a live payment provider.
export function projectedCredits(
  raw: number,
  ledger: number,
  publicUnit = 500000
): string {
  const numerator = BigInt(raw) * BigInt(publicUnit) * 10n ** 64n
  const denominator = BigInt(ledger)
  let result = numerator / denominator
  if ((numerator % denominator) * 2n >= denominator) result++
  const digits = result.toString().padStart(65, '0')
  const tail = digits.slice(-64).replace(/0+$/, '')
  return digits.slice(0, -64) + (tail ? `.${tail}` : '')
}
export function creditGrant(raw: number, ledger = 500000) {
  return {
    credit_unit_schema_version: 2,
    quota_unit: 'LEDGER_QUOTA',
    legacy_credit_unit: 'LEDGER_QUOTA',
    public_credit_unit: 'CREDIT',
    ledger_quota_per_usd: ledger,
    ledger_quota_per_usd_exact: String(ledger),
    public_credits_per_usd: 500000,
    public_credits_per_usd_exact: '500000',
    public_credit_metadata_version: 2,
    public_credit_amount_unit: 'CREDIT',
    credit_amount_unit: 'LEDGER_QUOTA',
    credit_amount: raw,
    credited_quota: raw,
    public_credit_amount: projectedCredits(raw, ledger),
  }
}
export function walletCatalog<
  T extends { pay_methods: Array<Record<string, unknown>> },
>(value: T, ledger = 500000): T & Record<string, unknown> {
  const row = value as Record<string, unknown>
  const options = (row.credit_amount_options ?? []) as number[]
  const discounts = (row.credit_discount ?? {}) as Record<string, number>
  const minimum = Number(row.credit_min_topup ?? 0)
  const pairs: Record<string, unknown> = {}
  for (const prefix of ['stripe_', 'waffo_', 'pancake_']) {
    const min = Number(row[`${prefix}credit_min_topup`] ?? 0)
    const max = row[`${prefix}credit_max_topup`] ?? null
    pairs[`${prefix}credit_min_topup`] = min
    pairs[`${prefix}credit_max_topup`] = max
    pairs[`${prefix}ledger_quota_min_topup`] = min
    pairs[`${prefix}public_credit_min_topup`] = projectedCredits(min, ledger)
    pairs[`${prefix}ledger_quota_max_topup`] = max
    pairs[`${prefix}public_credit_max_topup`] =
      max === null ? null : projectedCredits(Number(max), ledger)
  }
  return {
    ...value,
    ...creditGrant(1, ledger),
    ...pairs,
    credit_metadata_available: true,
    credit_metadata_version: 1,
    ledger_quota_amount_options: options,
    public_credit_amount_options: options.map((raw) =>
      projectedCredits(raw, ledger)
    ),
    ledger_quota_discount: discounts,
    public_credit_discount: Object.fromEntries(
      Object.entries(discounts).map(([raw, rate]) => [
        projectedCredits(Number(raw), ledger),
        rate,
      ])
    ),
    ledger_quota_min_topup: minimum,
    public_credit_min_topup: projectedCredits(minimum, ledger),
    pay_methods: value.pay_methods.map((method) => {
      const min = String(method.min_topup_credit ?? minimum)
      const max = method.max_topup_credit
      return {
        ...method,
        credit_amount_unit: 'LEDGER_QUOTA',
        min_topup_credit: min,
        min_topup_ledger_quota: min,
        min_topup_public_credit: projectedCredits(Number(min), ledger),
        ...(max === undefined
          ? {}
          : {
              max_topup_credit: String(max),
              max_topup_ledger_quota: String(max),
              max_topup_public_credit: projectedCredits(Number(max), ledger),
            }),
      }
    }),
  }
}
