/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */

// The existing store payment protocol quotes USD/CNY/LDC in hundredths and
// formats the original payment with the same scale. This is not an ISO currency
// precision default; a future payment currency must declare its own contract.
const STORE_PAYMENT_MINOR_SCALE = 100n
const fractionDigits = STORE_PAYMENT_MINOR_SCALE.toString().length - 1
const maxSafe = BigInt(Number.MAX_SAFE_INTEGER)

export function storeRefundNativeAmountSupported(currency: string): boolean {
  return currency === 'USD' || currency === 'CNY' || currency === 'LDC'
}

export function storeRefundNativeAmountInput(
  minor: number,
  currency: string
): string | undefined {
  if (
    !storeRefundNativeAmountSupported(currency) ||
    !Number.isSafeInteger(minor) ||
    minor < 0
  ) {
    return undefined
  }
  const exact = BigInt(minor)
  return `${exact / STORE_PAYMENT_MINOR_SCALE}.${(exact % STORE_PAYMENT_MINOR_SCALE).toString().padStart(fractionDigits, '0')}`
}

export function storeRefundNativeAmountFromInput(
  input: string,
  currency: string
): number | undefined {
  if (
    !storeRefundNativeAmountSupported(currency) ||
    input.length > 512 ||
    !/^\d+(?:\.\d+)?$/.test(input)
  ) {
    return undefined
  }
  const [whole, fraction = ''] = input.split('.')
  if (fraction.length > fractionDigits) return undefined
  const minor =
    BigInt(whole) * STORE_PAYMENT_MINOR_SCALE +
    BigInt(fraction.padEnd(fractionDigits, '0'))
  return minor > 0n && minor <= maxSafe ? Number(minor) : undefined
}

export function storeRefundNativeAmountText(
  minor: number,
  currency: string
): string | undefined {
  const amount = storeRefundNativeAmountInput(minor, currency)
  return amount === undefined ? undefined : `${amount} ${currency}`
}
