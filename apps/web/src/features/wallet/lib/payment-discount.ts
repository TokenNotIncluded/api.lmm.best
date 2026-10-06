/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { isFiatPaymentCurrency } from './format'

export interface PaymentDiscount {
  currency: string
  original: number
  paid: number
  savings: number
  percent: number
}
function money(
  value: unknown
): { text: string; digits: bigint; scale: number; amount: number } | null {
  if (
    typeof value !== 'string' ||
    value.length > 128 ||
    !/^\d+(?:\.\d+)?$/.test(value)
  ) {
    return null
  }
  const [whole, fraction = ''] = value.split('.')
  const amount = Number(value)
  if (!Number.isFinite(amount) || amount <= 0) return null
  return {
    text: value,
    digits: BigInt(whole + fraction),
    scale: fraction.length,
    amount,
  }
}

/** A breakdown is usable only with the exact server response it describes. */
export function parsePaymentDiscount(
  value: unknown,
  amount: unknown,
  currency: unknown
): PaymentDiscount | null {
  if (!value || typeof value !== 'object') return null
  const dto = value as Record<string, unknown>
  if (
    dto.schema_version !== 1 ||
    dto.basis !== 'amount_preset_and_code' ||
    !isFiatPaymentCurrency(dto.currency) ||
    dto.currency !== currency ||
    dto.paid_amount !== amount
  ) {
    return null
  }
  const original = money(dto.original_amount)
  const paid = money(dto.paid_amount)
  const savings = money(dto.savings_amount)
  if (!original || !paid || !savings) return null
  const scale = Math.max(original.scale, paid.scale, savings.scale)
  const scaled = (value: NonNullable<ReturnType<typeof money>>) =>
    value.digits * 10n ** BigInt(scale - value.scale)
  if (
    scaled(original) - scaled(paid) !== scaled(savings) ||
    scaled(original) <= scaled(paid)
  ) {
    return null
  }
  const percent = (savings.amount / original.amount) * 100
  const reported =
    typeof dto.discount_percent === 'string' &&
    dto.discount_percent.length <= 128 &&
    /^\d+(?:\.\d+)?$/.test(dto.discount_percent)
      ? Number(dto.discount_percent)
      : Number.NaN
  if (
    !Number.isFinite(percent) ||
    percent <= 0 ||
    percent >= 100 ||
    !Number.isFinite(reported) ||
    Math.abs(reported - percent) > 0.005000001
  ) {
    return null
  }
  return {
    currency: dto.currency,
    original: original.amount,
    paid: paid.amount,
    savings: savings.amount,
    percent,
  }
}

export function currentPaymentDiscount(
  value: PaymentDiscount | null | undefined,
  amount: number,
  currency: string | undefined,
  pending: boolean
): PaymentDiscount | null {
  return !pending &&
    value &&
    value.paid === amount &&
    value.currency === currency
    ? value
    : null
}

export function formatDiscountPercent(value: number, locale?: string): string {
  return value < 0.01
    ? '<0.01'
    : new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }).format(value)
}
