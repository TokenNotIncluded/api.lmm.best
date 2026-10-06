/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { z } from 'zod'

export type PaymentAmountUnit = 'USD' | 'CREDIT'
export const PAYMENT_AMOUNT_MAX_CREDITS = 9007199254740991n
const creditsPerUsd = 500000n
const numberToken = /^(0|[1-9]\d*)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/
const whitespace = '[ \\r\\n\\t]*'
const amountToken = new RegExp(`^${whitespace}(.+?)${whitespace}$`)

// Parse the original JSON token before any binary floating-point conversion.
// Canonical decimal strings remain exact while editors add/remove other rows.
export function normalizePaymentAmount(
  token: string,
  unit: PaymentAmountUnit = 'USD'
): string | null {
  if (token.length > 64) return null
  const match = numberToken.exec(token)
  if (!match) return null
  const exponent = BigInt(match[3] ?? '0')
  if (exponent < -18n || exponent > 18n) return null
  let coefficient = BigInt(match[1] + (match[2] ?? ''))
  if (coefficient === 0n) return null
  let scale = BigInt((match[2] ?? '').length) - exponent
  while (coefficient % 10n === 0n) {
    coefficient /= 10n
    scale -= 1n
  }
  if (scale > (unit === 'USD' ? 6n : 0n) || scale < -16n) return null
  const divisor = scale > 0n ? 10n ** scale : 1n
  const numerator =
    coefficient *
    (scale < 0n ? 10n ** -scale : 1n) *
    (unit === 'USD' ? creditsPerUsd : 1n)
  if (numerator % divisor !== 0n) return null
  const credits = numerator / divisor
  if (credits <= 0n || credits > PAYMENT_AMOUNT_MAX_CREDITS) return null
  const digits = coefficient.toString()
  const places = Number(scale)
  if (places <= 0) return digits + '0'.repeat(-places)
  if (places >= digits.length) {
    return `0.${'0'.repeat(places - digits.length)}${digits}`
  }
  return `${digits.slice(0, -places)}.${digits.slice(-places)}`
}

export function paymentAmountCredits(
  amount: string,
  unit: PaymentAmountUnit
): bigint {
  const [whole, fraction = ''] = amount.split('.')
  return (
    (BigInt(whole + fraction) * (unit === 'USD' ? creditsPerUsd : 1n)) /
    10n ** BigInt(fraction.length)
  )
}

export function parsePaymentAmountOptions(
  value: string,
  unit: PaymentAmountUnit = 'USD'
): string[] | null {
  const array = /^[ \r\n\t]*\[([\s\S]*)\][ \r\n\t]*$/.exec(value)
  if (!array) return null
  if (/^[ \r\n\t]*$/.test(array[1])) return []
  const tokens = array[1].split(',')
  if (tokens.length > 100) return null
  const amounts: string[] = []
  for (const token of tokens) {
    const raw = amountToken.exec(token)?.[1]
    const amount = raw ? normalizePaymentAmount(raw, unit) : null
    if (amount === null || amounts.includes(amount)) return null
    amounts.push(amount)
  }
  return amounts
}

export function serializePaymentAmountOptions(amounts: string[]): string {
  return `[${amounts.join(',')}]`
}

export function normalizePaymentAmountOptionsJson(
  value: string,
  unit: PaymentAmountUnit
): string {
  const amounts = parsePaymentAmountOptions(value, unit)
  return amounts === null ? value : serializePaymentAmountOptions(amounts)
}

export function formatPaymentAmountOptionsJson(
  value: string,
  unit: PaymentAmountUnit
): string | null {
  const amounts = parsePaymentAmountOptions(value, unit)
  if (amounts === null) return null
  return amounts.length === 0 ? '[]' : `[\n  ${amounts.join(',\n  ')}\n]`
}

export function isPositiveSafeAmount(
  value: unknown,
  unit: PaymentAmountUnit = 'USD'
): value is number {
  return (
    typeof value === 'number' &&
    Number.isFinite(value) &&
    normalizePaymentAmount(String(value), unit) !== null
  )
}

export function isPaymentAmountOptions(
  value: unknown,
  unit: PaymentAmountUnit = 'USD'
): value is number[] {
  return (
    Array.isArray(value) &&
    value.length <= 100 &&
    value.every((amount) => isPositiveSafeAmount(amount, unit)) &&
    new Set(value.map(String)).size === value.length
  )
}

export function isPaymentAmountInput(
  value: string,
  unit: PaymentAmountUnit = 'USD'
): boolean {
  return normalizePaymentAmount(value, unit) !== null
}

export const createPaymentAmountOptionsSchema = (unit: PaymentAmountUnit) =>
  z.string().transform((value, ctx) => {
    const amounts = parsePaymentAmountOptions(value, unit)
    if (amounts === null) {
      ctx.addIssue({ code: 'custom', message: 'JSON structure is invalid' })
      return z.NEVER
    }
    return serializePaymentAmountOptions(amounts)
  })

export const paymentAmountOptionsSchema =
  createPaymentAmountOptionsSchema('USD')

export function parsePaymentAmountDiscounts(
  value: string,
  unit: PaymentAmountUnit
): Record<string, number> | null {
  const object = /^[ \r\n\t]*\{([\s\S]*)\}[ \r\n\t]*$/.exec(value)
  if (!object) return null
  let remaining = object[1]
  const result: Record<string, number> = {}
  if (/^[ \r\n\t]*$/.test(remaining)) return result
  // Keys stay strings. Read every pair, including duplicate spellings, before
  // JSON object parsing could discard a conflicting rate for the same amount.
  const pair =
    /^[ \r\n\t]*("(?:[^"\\]|\\(?:["\\/bfnrt]|u[0-9a-fA-F]{4}))*")[ \r\n\t]*:[ \r\n\t]*(-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)[ \r\n\t]*(,|$)/
  while (remaining) {
    const match = pair.exec(remaining)
    if (!match) return null
    let key: string
    try {
      key = JSON.parse(match[1]) as string
    } catch {
      return null
    }
    // Match the backend key grammar, retaining legacy +1/leading-zero keys.
    if (key.length > 64) return null
    if (!/^\+?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?$/.test(key)) return null
    const legacyKey = key.replace(/^\+/, '').replace(/^0+(?=\d)/, '')
    const amount = normalizePaymentAmount(legacyKey, unit)
    const rate = Number(match[2])
    if (amount === null || !Number.isFinite(rate) || rate <= 0 || rate > 1) {
      return null
    }
    if (Object.hasOwn(result, amount) && result[amount] !== rate) return null
    result[amount] = rate
    remaining = remaining.slice(match[0].length)
    if (match[3] === ',' && !remaining.trim()) return null
  }
  return result
}

export const createPaymentAmountDiscountSchema = (unit: PaymentAmountUnit) =>
  z
    .string()
    .refine(
      (value) => parsePaymentAmountDiscounts(value, unit) !== null,
      'JSON structure is invalid'
    )
    .transform((value) =>
      JSON.stringify(parsePaymentAmountDiscounts(value, unit))
    )

export function normalizePaymentAmountDiscountJson(
  value: string,
  unit: PaymentAmountUnit
): string {
  const discounts = parsePaymentAmountDiscounts(value, unit)
  return discounts === null ? value : JSON.stringify(discounts)
}

export function formatPaymentAmountDiscountJson(
  value: string,
  unit: PaymentAmountUnit
): string | null {
  const discounts = parsePaymentAmountDiscounts(value, unit)
  return discounts === null ? null : JSON.stringify(discounts, null, 2)
}
