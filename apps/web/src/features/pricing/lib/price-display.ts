/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { formatUSDInCurrency, type CurrencyFormatOptions } from '@/lib/currency'

import type { PriceDisplayCurrency } from '../types'

/** Prices arrive in real USD. Shared display conversion never changes billing. */
export function formatModelPrice(
  amountUSD: number,
  currency: PriceDisplayCurrency,
  options: CurrencyFormatOptions = {}
): string {
  if (!Number.isFinite(amountUSD) || amountUSD < 0) return '-'
  return formatUSDInCurrency(amountUSD, currency, {
    digitsLarge: 4,
    digitsSmall: 6,
    abbreviate: false,
    ...options,
  })
}
