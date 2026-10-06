/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { storeQuantity } from './quantity'

// Empty means unlimited. Zero, fractional values and unsafe integers cannot
// silently become an unlimited setting.
export function storePurchaseLimit(value: string): number | null | undefined {
  const trimmed = value.trim()
  return trimmed === '' ? null : storeQuantity(trimmed)
}
