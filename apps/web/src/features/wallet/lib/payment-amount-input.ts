/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */

import { toIntlLocale } from '@/i18n/languages'

/** A short, read-only display. The exact draft is restored before editing. */
export function formatFiatAmountInput(
  exactValue: string,
  locale?: string
): string {
  if (!/^\d+(?:\.\d+)?$/.test(exactValue)) return exactValue
  const amount = Number(exactValue)
  if (!Number.isFinite(amount) || amount < 0) return exactValue
  const digits =
    amount > 0 && amount < 1
      ? Math.min(20, Math.max(2, Math.ceil(-Math.log10(amount)) + 1))
      : 2
  const rounded = Number(amount.toFixed(digits))
  if (amount > 0 && rounded === 0) return `≈${amount.toString()}`
  const formatted = new Intl.NumberFormat(
    locale ? toIntlLocale(locale) : undefined,
    {
      useGrouping: false,
      maximumFractionDigits: digits,
    }
  ).format(rounded)
  return rounded === amount ? formatted : `≈${formatted}`
}
