/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { parseDiscountCodeMaxUses } from './availability'

export const DISCOUNT_CODE_LIMITS = {
  nameMaxLength: 120,
  codeMinLength: 3,
  codeMaxLength: 64,
  batchMin: 1,
  batchMax: 100,
  percentMin: 1,
  percentMax: 99,
} as const

export type DiscountCodeFormValues = {
  code: string
  name: string
  count: string
  discount_percent: string
  min_amount: string
  max_uses: string
  starts_time: string
  expired_time: string
}

export type DiscountCodeFormErrors = Partial<
  Record<keyof DiscountCodeFormValues, string>
>

/**
 * Field-level errors for the create/edit form. Returned keys are i18n keys so
 * the caller owns the copy, and callers can render each message next to its
 * own input instead of a single generic banner.
 */
export function validateDiscountCodeForm(
  values: DiscountCodeFormValues,
  options: { editing: boolean }
): DiscountCodeFormErrors {
  const errors: DiscountCodeFormErrors = {}

  if (options.editing) {
    if (values.code.trim().length < DISCOUNT_CODE_LIMITS.codeMinLength) {
      errors.code = 'Codes must be at least 3 characters.'
    }
  } else {
    const count = Number(values.count)
    if (
      !Number.isInteger(count) ||
      count < DISCOUNT_CODE_LIMITS.batchMin ||
      count > DISCOUNT_CODE_LIMITS.batchMax
    ) {
      errors.count = 'Enter a whole number between 1 and 100.'
    }
  }

  if (values.name.trim().length === 0) {
    errors.name = 'Give this discount a name so it is recognisable later.'
  }

  const percent = Number(values.discount_percent)
  if (
    !/^\d+$/.test(values.discount_percent.trim()) ||
    percent < DISCOUNT_CODE_LIMITS.percentMin ||
    percent > DISCOUNT_CODE_LIMITS.percentMax
  ) {
    errors.discount_percent = 'Enter a whole percentage between 1 and 99.'
  }

  const minAmount = values.min_amount.trim()
  if (minAmount !== '' && !/^\d+$/.test(minAmount)) {
    errors.min_amount = 'Enter a whole number of 0 or more.'
  }

  if (parseDiscountCodeMaxUses(values.max_uses) === undefined) {
    errors.max_uses = 'Enter a whole number. Use 0 for unlimited.'
  }

  const starts = values.starts_time ? new Date(values.starts_time) : null
  const expires = values.expired_time ? new Date(values.expired_time) : null
  if (starts && Number.isNaN(starts.getTime())) {
    errors.starts_time = 'Enter a valid start date.'
  }
  if (expires && Number.isNaN(expires.getTime())) {
    errors.expired_time = 'Enter a valid expiry date.'
  }
  if (
    starts &&
    expires &&
    !Number.isNaN(starts.getTime()) &&
    !Number.isNaN(expires.getTime()) &&
    expires.getTime() <= starts.getTime()
  ) {
    errors.expired_time = 'Expiry must be later than the start time.'
  }

  return errors
}

export function isDiscountCodeFormValid(errors: DiscountCodeFormErrors) {
  return Object.keys(errors).length === 0
}
