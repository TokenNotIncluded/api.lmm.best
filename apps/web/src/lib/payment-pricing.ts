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
import type { CurrencyConfig } from '@/stores/system-config-store'

const DEDICATED_PAYMENT_PRICING_TYPES = new Set([
  'stripe',
  'waffo',
  'waffo_pancake',
  'alipay',
  'wxpay',
])

export function usesDedicatedPaymentPricing(type?: string): boolean {
  return !!type && DEDICATED_PAYMENT_PRICING_TYPES.has(type)
}

/** Convert a legacy recharge batch amount using an explicit fixed denomination. */
export function platformUnitsToUsd(
  amount: number,
  platformUnitsPerUSD: number
): number {
  if (
    !Number.isFinite(amount) ||
    amount < 0 ||
    !Number.isFinite(platformUnitsPerUSD) ||
    platformUnitsPerUSD <= 0
  ) {
    return Number.NaN
  }
  return amount / platformUnitsPerUSD
}

type Fraction = { numerator: bigint; denominator: bigint }

/** Decimal arithmetic is exact: stored integer thresholds must never be rounded. */
function decimalFraction(value: number | string): Fraction | null {
  if (typeof value === 'number' && !Number.isFinite(value)) return null
  const source = String(value).trim()
  if (!source || source.length > 512) return null
  const match = source.match(/^([+]?)(\d*)(?:\.(\d*))?(?:e([+-]?\d+))?$/i)
  if (!match || !(match[2] || match[3])) return null
  const exponent = Number(match[4] || 0) - (match[3]?.length || 0)
  if (!Number.isInteger(exponent) || Math.abs(exponent) > 400) return null
  const numerator = BigInt((match[2] || '0') + (match[3] || ''))
  return exponent >= 0
    ? { numerator: numerator * 10n ** BigInt(exponent), denominator: 1n }
    : { numerator, denominator: 10n ** BigInt(-exponent) }
}

function fixedLegacyUnitsPerUsd(config: CurrencyConfig): Fraction | null {
  const credits = config.creditsPerUsd
  const batch = config.quotaPerUnit
  if (
    config.currencyUnit !== 'credit' ||
    !Number.isFinite(credits) ||
    !credits ||
    credits <= 0 ||
    !Number.isFinite(batch) ||
    batch <= 0
  ) {
    return null
  }
  const exact = config.creditsPerUsdExact
  const denomination = decimalFraction(
    exact && Number(exact) === credits ? exact : credits
  )
  const legacyBatch = decimalFraction(batch)
  if (!denomination || !legacyBatch || legacyBatch.numerator <= 0n) return null
  return {
    numerator: denomination.numerator * legacyBatch.denominator,
    denominator: denomination.denominator * legacyBatch.numerator,
  }
}

function decimalProjection(value: Fraction): string {
  const precision = 30n
  const scale = 10n ** precision
  const scaled = value.numerator * scale
  let rounded = scaled / value.denominator
  if (scaled % value.denominator !== 0n) rounded += 1n
  const text = rounded.toString().padStart(31, '0')
  return `${text.slice(0, -30)}.${text.slice(-30)}`
    .replace(/0+$/, '')
    .replace(/\.$/, '')
}

/** Presentation only. The untouched legacy integer remains the source of truth. */
export function legacyMinimumToUsdInput(
  legacyAmount: number,
  config: CurrencyConfig
): string {
  const rate = fixedLegacyUnitsPerUsd(config)
  if (!Number.isSafeInteger(legacyAmount) || legacyAmount < 0 || !rate) {
    return ''
  }
  return decimalProjection({
    numerator: BigInt(legacyAmount) * rate.denominator,
    denominator: rate.numerator,
  })
}

/** A new USD minimum must map exactly to the integer legacy storage field. */
export function usdToLegacyMinimum(
  amount: number | string,
  config: CurrencyConfig
): number {
  const value = decimalFraction(amount)
  const rate = fixedLegacyUnitsPerUsd(config)
  if (!value || !rate) return Number.NaN
  const numerator = value.numerator * rate.numerator
  const denominator = value.denominator * rate.denominator
  if (numerator % denominator !== 0n) return Number.NaN
  const integer = numerator / denominator
  return integer <= BigInt(Number.MAX_SAFE_INTEGER)
    ? Number(integer)
    : Number.NaN
}

/** Native gateway units charged for one real USD of credited value. No FX is implied. */
export function legacySettlementRatePerUsd(
  directRate: string,
  config: CurrencyConfig
): string {
  const value = decimalFraction(directRate)
  const rate = fixedLegacyUnitsPerUsd(config)
  if (!value || value.numerator <= 0n || !rate) return ''
  return decimalProjection({
    numerator: value.numerator * rate.numerator,
    denominator: value.denominator * rate.denominator,
  })
}

/** Match the Epay protocol default without inventing a fiat unit for LinuxDO Credit. */
export function getLegacyGatewaySettlementUnit(
  type: string,
  name: string,
  configuredUnit?: string
): string {
  if (type === 'alipay' || type === 'wxpay') return 'CNY'
  if (configuredUnit?.trim()) return configuredUnit.trim()
  const normalizedName = name.toLowerCase()
  if (
    type === 'epay' &&
    ['ldc', 'linuxdo', 'linux do'].some((marker) =>
      normalizedName.includes(marker)
    )
  ) {
    return ''
  }
  return 'CNY'
}
