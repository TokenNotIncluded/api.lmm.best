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
import { usesDedicatedPaymentPricing } from '@/lib/payment-pricing'

import type { PaymentMethod } from '../types'

const SETTLEMENT_UNIT_PATTERN = /^[A-Za-z0-9._-]{1,16}$/
const POSITIVE_DECIMAL_PATTERN = /^[0-9]+(?:\.[0-9]+)?$/

export type PaymentSettlementMetadata = {
  currencyCode: string
  platformUnitsPerUsd: number
  settlementUnitsPerUsd: number
  source: 'explicit-usd-rates' | 'legacy-unit-price' | 'legacy-usd-price-ratio'
}

/** @deprecated Use PaymentSettlementMetadata in new wallet code. */
export type PaymentSettlementUnit = {
  label: string
  unitPrice: number
}

function parsePositiveDecimal(value: unknown): number | null {
  if (typeof value === 'string') {
    if (!POSITIVE_DECIMAL_PATTERN.test(value)) return null
    const parsed = Number(value)
    return Number.isFinite(parsed) && parsed > 0 ? parsed : null
  }

  return typeof value === 'number' && Number.isFinite(value) && value > 0
    ? value
    : null
}

function formatRate(value: string | number | undefined, normalized: number) {
  return typeof value === 'string'
    ? value
    : new Intl.NumberFormat('en-US', {
        maximumFractionDigits: 20,
        useGrouping: false,
      }).format(normalized)
}

/**
 * Normalize the server-owned maximum credited USD for one payment. Invalid
 * metadata is ignored in the UI; the backend still fails closed at checkout.
 */
export function getPaymentMaxTopup(
  paymentMethod?: PaymentMethod
): number | null {
  const rawLimit = paymentMethod?.max_topup
  if (typeof rawLimit === 'string') {
    if (!POSITIVE_DECIMAL_PATTERN.test(rawLimit)) return null
    const parsedLimit = Number(rawLimit)
    return Number.isFinite(parsedLimit) && parsedLimit > 0 ? parsedLimit : null
  }

  return typeof rawLimit === 'number' &&
    Number.isFinite(rawLimit) &&
    rawLimit > 0
    ? rawLimit
    : null
}

/**
 * Normalize a server-owned per-method payment multiplier. Missing or invalid
 * metadata keeps legacy payment methods at the neutral multiplier of 1.
 */
export function getPaymentTopupRatio(paymentMethod?: PaymentMethod): number {
  if (usesDedicatedPaymentPricing(paymentMethod?.type)) return 1

  const rawRatio = paymentMethod?.topup_ratio
  if (typeof rawRatio === 'string') {
    if (!POSITIVE_DECIMAL_PATTERN.test(rawRatio)) return 1
    const parsedRatio = Number(rawRatio)
    return Number.isFinite(parsedRatio) && parsedRatio > 0 ? parsedRatio : 1
  }

  return typeof rawRatio === 'number' &&
    Number.isFinite(rawRatio) &&
    rawRatio > 0
    ? rawRatio
    : 1
}

/**
 * Normalize server-owned gateway settlement metadata. The preferred contract
 * carries both sides of the USD bridge explicitly:
 * settlement = platform / platformUnitsPerUsd * settlementUnitsPerUsd.
 *
 * Older servers may send settlement_unit + unit_price. That fallback is
 * deliberately isolated and interprets unit_price as settlement units per
 * platform unit; incomplete preferred metadata never falls through to it.
 */
export function getPaymentSettlementMetadata(
  paymentMethod?: PaymentMethod,
  includeDedicated = false
): PaymentSettlementMetadata | null {
  if (!includeDedicated && usesDedicatedPaymentPricing(paymentMethod?.type)) {
    return null
  }

  const hasPreferredMetadata =
    paymentMethod?.platform_units_per_usd !== undefined ||
    paymentMethod?.settlement_units_per_usd !== undefined
  const currencyCode =
    paymentMethod?.settlement_currency ?? paymentMethod?.settlement_unit

  if (hasPreferredMetadata) {
    const platformUnitsPerUsd = parsePositiveDecimal(
      paymentMethod?.platform_units_per_usd
    )
    const settlementUnitsPerUsd = parsePositiveDecimal(
      paymentMethod?.settlement_units_per_usd
    )
    if (
      !currencyCode ||
      !SETTLEMENT_UNIT_PATTERN.test(currencyCode) ||
      platformUnitsPerUsd === null ||
      settlementUnitsPerUsd === null
    ) {
      return null
    }

    return {
      currencyCode: currencyCode.toUpperCase(),
      platformUnitsPerUsd,
      settlementUnitsPerUsd,
      source: 'explicit-usd-rates',
    }
  }

  const explicitDirectRate = parsePositiveDecimal(
    paymentMethod?.settlement_units_per_platform_unit
  )
  const legacyDirectRate = parsePositiveDecimal(paymentMethod?.unit_price)
  if (
    explicitDirectRate !== null &&
    legacyDirectRate !== null &&
    explicitDirectRate !== legacyDirectRate
  ) {
    return null
  }
  const settlementUnitsPerPlatformUnit = explicitDirectRate ?? legacyDirectRate
  if (
    !currencyCode ||
    !SETTLEMENT_UNIT_PATTERN.test(currencyCode) ||
    settlementUnitsPerPlatformUnit === null
  ) {
    return null
  }

  return {
    currencyCode: currencyCode.toUpperCase(),
    platformUnitsPerUsd: 1,
    settlementUnitsPerUsd: settlementUnitsPerPlatformUnit,
    source: 'legacy-unit-price',
  }
}

export function calculateSettlementAmount(
  platformAmount: number,
  metadata: PaymentSettlementMetadata
): number {
  return (
    (platformAmount / metadata.platformUnitsPerUsd) *
    metadata.settlementUnitsPerUsd
  )
}

/**
 * Last-resort contract for old top-up responses that publish no gateway
 * metadata. It is intentionally fixed to real USD and never reads the global
 * display-currency setting.
 */
export function createLegacyUsdSettlementMetadata(
  settlementUnitsPerPlatformUnit: number
): PaymentSettlementMetadata {
  const normalizedRate =
    Number.isFinite(settlementUnitsPerPlatformUnit) &&
    settlementUnitsPerPlatformUnit > 0
      ? settlementUnitsPerPlatformUnit
      : 1
  return {
    currencyCode: 'USD',
    platformUnitsPerUsd: 1,
    settlementUnitsPerUsd: normalizedRate,
    source: 'legacy-usd-price-ratio',
  }
}

/** @deprecated Compatibility adapter for older wallet imports. */
export function getPaymentSettlementUnit(
  paymentMethod?: PaymentMethod,
  includeDedicated = false
): PaymentSettlementUnit | null {
  const metadata = getPaymentSettlementMetadata(paymentMethod, includeDedicated)
  if (!metadata) return null
  return {
    label: metadata.currencyCode,
    unitPrice: metadata.settlementUnitsPerUsd / metadata.platformUnitsPerUsd,
  }
}

/** Format both sides of the configured settlement contract. */
export function formatPaymentSettlementRate(
  paymentMethod?: PaymentMethod,
  platformCurrencyLabel = 'USD',
  includeDedicated = false
): string | null {
  const metadata = getPaymentSettlementMetadata(paymentMethod, includeDedicated)
  if (!metadata) return null

  if (metadata.source !== 'explicit-usd-rates') {
    return `${formatRate(paymentMethod?.unit_price, metadata.settlementUnitsPerUsd)} ${metadata.currencyCode} / ${platformCurrencyLabel}`
  }

  const platformRate = formatRate(
    paymentMethod?.platform_units_per_usd,
    metadata.platformUnitsPerUsd
  )
  const settlementRate = formatRate(
    paymentMethod?.settlement_units_per_usd,
    metadata.settlementUnitsPerUsd
  )
  return `${settlementRate} ${metadata.currencyCode} / ${platformRate} ${platformCurrencyLabel}`
}

export function formatSettlementAmount(amount: number, unit: string): string {
  const maximumFractionDigits = Math.abs(amount) >= 1 ? 2 : 4
  const formatted = new Intl.NumberFormat(undefined, {
    maximumFractionDigits,
  }).format(amount)
  const settlementUnit = unit.trim().toUpperCase() || 'USD'
  return `${formatted} ${settlementUnit}`
}
