/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { z } from 'zod'

export function isPositiveSafeAmount(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0
}

export function isPaymentAmountOptions(value: unknown): value is number[] {
  return Array.isArray(value) && value.every(isPositiveSafeAmount)
}

export function isPaymentAmountInput(value: string): boolean {
  return /^[1-9]\d*$/.test(value) && isPositiveSafeAmount(Number(value))
}

export const paymentAmountOptionsSchema = z
  .string()
  .refine((value) => {
    try {
      // Validate integer tokens before JSON.parse can round a large fraction
      // into a safe integer. Match Go's []int decoder, including JSON mode.
      return (
        /^\[\s*(?:[1-9]\d*\s*(?:,\s*[1-9]\d*\s*)*)?\]$/.test(value.trim()) &&
        isPaymentAmountOptions(JSON.parse(value))
      )
    } catch {
      return false
    }
  }, 'JSON structure is invalid')
  .transform((value) => JSON.stringify(JSON.parse(value)))
