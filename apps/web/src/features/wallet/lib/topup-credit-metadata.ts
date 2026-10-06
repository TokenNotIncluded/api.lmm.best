/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
type Fields = Record<string, unknown>
const MAX_QUOTA = BigInt(Number.MAX_SAFE_INTEGER)
const PUBLIC_PRECISION = 10n ** 64n

function fields(value: unknown): value is Fields {
  return !!value && typeof value === 'object' && !Array.isArray(value)
}

function quota(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
}

function quotaString(value: unknown): number | null {
  if (typeof value !== 'string' || !/^(0|[1-9]\d{0,15})$/.test(value)) {
    return null
  }
  const parsed = Number(value)
  return quota(parsed) ? parsed : null
}

/** Credit aliases must equal raw wallet points at the fixed 500,000/USD basis. */
export function creditProjection(
  value: unknown
): ((rawQuota: number) => string) | null {
  if (
    !fields(value) ||
    value.credit_unit_schema_version !== 2 ||
    value.quota_unit !== 'LEDGER_QUOTA' ||
    value.legacy_credit_unit !== 'LEDGER_QUOTA' ||
    value.public_credit_unit !== 'CREDIT'
  ) {
    return null
  }
  const {
    ledger_quota_per_usd_exact: ledger,
    public_credits_per_usd_exact: publicUnit,
  } = value
  if (
    ledger !== '500000' ||
    publicUnit !== '500000' ||
    typeof ledger !== 'string' ||
    ledger.length > 80 ||
    !/^(0|[1-9]\d*)(?:\.\d{1,18})?$/.test(ledger) ||
    typeof value.ledger_quota_per_usd !== 'number' ||
    !Number.isFinite(value.ledger_quota_per_usd) ||
    value.ledger_quota_per_usd <= 0 ||
    value.ledger_quota_per_usd > Number.MAX_SAFE_INTEGER ||
    Number(ledger) !== value.ledger_quota_per_usd ||
    quotaString(publicUnit) === null ||
    Number(publicUnit) <= 0 ||
    Number(publicUnit) !== value.public_credits_per_usd
  ) {
    return null
  }
  const [integer, fraction = ''] = ledger.split('.')
  const denominator = BigInt(integer + fraction)
  if (
    denominator <= 0n ||
    denominator > MAX_QUOTA * 10n ** BigInt(fraction.length)
  ) {
    return null
  }
  const numerator =
    BigInt(publicUnit as string) * 10n ** BigInt(fraction.length)
  return (rawQuota) => {
    if (!quota(rawQuota) || BigInt(rawQuota) > MAX_QUOTA) {
      throw new Error('Invalid ledger quota')
    }
    const scaled = BigInt(rawQuota) * numerator * PUBLIC_PRECISION
    let rounded = scaled / denominator
    if ((scaled % denominator) * 2n >= denominator) rounded++
    const digits = rounded.toString().padStart(65, '0')
    const tail = digits.slice(-64).replace(/0+$/, '')
    return digits.slice(0, -64) + (tail ? `.${tail}` : '')
  }
}

function sameDiscounts(left: unknown, right: unknown): boolean {
  if (!fields(left) || !fields(right)) return false
  const entries = Object.entries(left)
  return (
    entries.length === Object.keys(right).length &&
    entries.every(
      ([key, rate]) =>
        quotaString(key) !== null &&
        Number(key) > 0 &&
        typeof rate === 'number' &&
        Number.isFinite(rate) &&
        rate > 0 &&
        right[key] === rate
    )
  )
}

/** A catalog is usable only when every raw/public alias belongs to the captured basis. */
export function hasCompletePublicCreditCatalog(value: unknown): boolean {
  if (
    !fields(value) ||
    value.credit_metadata_available !== true ||
    value.credit_metadata_version !== 1 ||
    value.public_credit_metadata_version !== 2 ||
    value.public_credit_amount_unit !== 'CREDIT'
  ) {
    return false
  }
  const project = creditProjection(value)
  if (!project) return false
  const options = value.ledger_quota_amount_options
  const publicOptions = value.public_credit_amount_options
  const oldOptions = value.credit_amount_options
  if (
    !Array.isArray(options) ||
    !Array.isArray(publicOptions) ||
    !Array.isArray(oldOptions) ||
    options.length !== publicOptions.length ||
    options.length !== oldOptions.length ||
    !options.every(
      (raw, index) =>
        quota(raw) &&
        raw > 0 &&
        oldOptions[index] === raw &&
        publicOptions[index] === project(raw)
    )
  ) {
    return false
  }
  if (
    !sameDiscounts(value.ledger_quota_discount, value.credit_discount) ||
    !fields(value.ledger_quota_discount) ||
    !fields(value.public_credit_discount)
  ) {
    return false
  }
  const discounts = value.ledger_quota_discount
  const publicDiscounts = value.public_credit_discount
  if (
    Object.keys(discounts).length !== Object.keys(publicDiscounts).length ||
    !Object.entries(discounts).every(
      ([raw, rate]) => publicDiscounts[project(Number(raw))] === rate
    )
  ) {
    return false
  }
  for (const prefix of ['', 'stripe_', 'waffo_', 'pancake_']) {
    const minimum = value[`${prefix}ledger_quota_min_topup`]
    if (
      !quota(minimum) ||
      value[`${prefix}credit_min_topup`] !== minimum ||
      value[`${prefix}public_credit_min_topup`] !== project(minimum)
    ) {
      return false
    }
    if (prefix) {
      const maximum = value[`${prefix}ledger_quota_max_topup`]
      if (maximum !== null && (!quota(maximum) || maximum < minimum)) {
        return false
      }
      if (
        value[`${prefix}credit_max_topup`] !== maximum ||
        value[`${prefix}public_credit_max_topup`] !==
          (maximum === null ? null : project(maximum as number))
      ) {
        return false
      }
    }
  }
  let methods: unknown = value.pay_methods
  if (typeof methods === 'string') {
    try {
      methods = JSON.parse(methods)
    } catch {
      return false
    }
  }
  if (!Array.isArray(methods)) return false
  return methods.every((method) => {
    // Invalid rows cannot become payment choices; preserve the existing catalog sanitization.
    if (
      !fields(method) ||
      typeof method.name !== 'string' ||
      !method.name ||
      typeof method.type !== 'string' ||
      !method.type
    ) {
      return true
    }
    const minimum = quotaString(method.min_topup_ledger_quota)
    if (
      method.credit_amount_unit !== 'LEDGER_QUOTA' ||
      minimum === null ||
      method.min_topup_credit !== method.min_topup_ledger_quota ||
      method.min_topup_public_credit !== project(minimum)
    ) {
      return false
    }
    const maximum = method.max_topup_ledger_quota
    if (maximum === undefined) {
      return (
        method.max_topup_credit === undefined &&
        method.max_topup_public_credit === undefined
      )
    }
    const parsed = quotaString(maximum)
    return (
      parsed !== null &&
      parsed >= minimum &&
      method.max_topup_credit === maximum &&
      method.max_topup_public_credit === project(parsed)
    )
  })
}

/** Success responses must identify the grant; fiat quote strings are left untouched. */
export function hasCompleteCreditGrant(
  value: unknown,
  expectedQuota?: number,
  expectedPublicCredits?: number
): boolean {
  if (
    !fields(value) ||
    value.public_credit_metadata_version !== 2 ||
    value.credit_amount_unit !== 'LEDGER_QUOTA' ||
    value.public_credit_amount_unit !== 'CREDIT'
  ) {
    return false
  }
  const project = creditProjection(value)
  if (project && expectedPublicCredits !== undefined) {
    if (!quota(expectedPublicCredits) || expectedPublicCredits <= 0) {
      return false
    }
    const [integer, fraction = ''] = (
      value.ledger_quota_per_usd_exact as string
    ).split('.')
    const raw =
      (BigInt(expectedPublicCredits) * BigInt(integer + fraction)) /
      (BigInt(value.public_credits_per_usd_exact as string) *
        10n ** BigInt(fraction.length))
    if (raw <= 0n || raw > MAX_QUOTA || value.credited_quota !== Number(raw)) {
      return false
    }
  }
  return (
    !!project &&
    quota(value.credited_quota) &&
    value.credited_quota > 0 &&
    value.credit_amount === value.credited_quota &&
    (expectedQuota === undefined || value.credited_quota === expectedQuota) &&
    value.public_credit_amount === project(value.credited_quota)
  )
}
