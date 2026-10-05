/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { z } from 'zod'

export const signedCreditAmountSchema = z
  .number()
  .int()
  .min(-Number.MAX_SAFE_INTEGER)
  .max(Number.MAX_SAFE_INTEGER)
export const creditAmountSchema = signedCreditAmountSchema.min(0)

export function isCreditAmount(
  value: unknown,
  allowNegative = false
): value is number {
  return (
    typeof value === 'number' &&
    Number.isSafeInteger(value) &&
    (allowNegative || value >= 0)
  )
}

export function assertCreditAmount(
  value: number,
  allowNegative = false
): number {
  if (!isCreditAmount(value, allowNegative))
    throw new RangeError(
      'Enter a valid amount within the supported credit range'
    )
  return value
}
