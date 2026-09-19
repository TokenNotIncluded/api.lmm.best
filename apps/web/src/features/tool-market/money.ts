/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
export function creditAmount(quota: number, units: number) {
  return new Intl.NumberFormat(undefined, { maximumFractionDigits: 6 }).format(
    quota / units
  )
}

export function marketQuota(raw: string, units: number): number {
  if (
    raw.length > 64 ||
    !/^\d+(\.\d{1,6})?$/.test(raw.trim()) ||
    !Number.isSafeInteger(units) ||
    units <= 0
  ) {
    throw new Error('Invalid amount')
  }
  const [whole, fraction = ''] = raw.trim().split('.')
  const scale = 10n ** BigInt(fraction.length)
  const numerator =
    (BigInt(whole) * scale + BigInt(fraction || '0')) * BigInt(units)
  const quota = numerator / scale
  if (numerator % scale !== 0n || quota > BigInt(Number.MAX_SAFE_INTEGER)) {
    throw new Error('Invalid amount')
  }
  return Number(quota)
}
