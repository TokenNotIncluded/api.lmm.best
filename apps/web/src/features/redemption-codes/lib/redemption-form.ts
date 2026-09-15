/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { TFunction } from 'i18next'
import { z } from 'zod'

import { parseQuotaFromDollars, quotaUnitsToEditableAmount } from '@/lib/format'

import {
  REDEMPTION_VALIDATION,
  getRedemptionFormErrorMessages,
} from '../constants'
import type {
  RedemptionFormData,
  Redemption,
  RedemptionRewardType,
} from '../types'

// ============================================================================
// Form Schema (use getRedemptionFormSchema(t) in components for i18n messages)
// ============================================================================

export function getRedemptionFormSchema(t: TFunction) {
  const msg = getRedemptionFormErrorMessages(t)
  return z
    .object({
      name: z
        .string()
        .min(REDEMPTION_VALIDATION.NAME_MIN_LENGTH, msg.NAME_LENGTH_INVALID)
        .max(REDEMPTION_VALIDATION.NAME_MAX_LENGTH, msg.NAME_LENGTH_INVALID),
      quota_dollars: z.number().min(0, t('Quota must be a positive number')),
      reward_type: z.enum(['quota', 'reset_voucher']),
      reset_plan_id: z.number().min(0),
      reset_voucher_expires_at: z.date().optional(),
      expired_time: z.date().optional(),
      count: z
        .number()
        .min(REDEMPTION_VALIDATION.COUNT_MIN, msg.COUNT_INVALID)
        .max(REDEMPTION_VALIDATION.COUNT_MAX, msg.COUNT_INVALID)
        .optional(),
    })
    .superRefine((data, ctx) => {
      if (data.reward_type === 'reset_voucher' && data.reset_plan_id <= 0) {
        ctx.addIssue({
          code: 'custom',
          path: ['reset_plan_id'],
          message: t('Choose a subscription plan for the banked reset voucher'),
        })
      }
      if (
        data.reward_type === 'reset_voucher' &&
        data.reset_voucher_expires_at &&
        data.reset_voucher_expires_at.getTime() <= Date.now()
      ) {
        ctx.addIssue({
          code: 'custom',
          path: ['reset_voucher_expires_at'],
          message: t('Voucher expiration must be in the future'),
        })
      }
    })
}

export type RedemptionFormValues = {
  name: string
  quota_dollars: number
  reward_type: RedemptionRewardType
  reset_plan_id: number
  reset_voucher_expires_at?: Date
  expired_time?: Date
  count?: number
}

// ============================================================================
// Form Defaults
// ============================================================================

export const REDEMPTION_FORM_DEFAULT_VALUES: RedemptionFormValues = {
  name: '',
  quota_dollars: 10,
  reward_type: 'quota',
  reset_plan_id: 0,
  reset_voucher_expires_at: undefined,
  expired_time: undefined,
  count: 1,
}

// ============================================================================
// Form Data Transformation
// ============================================================================

export function transformFormDataToPayload(
  data: RedemptionFormValues
): RedemptionFormData {
  const isResetVoucher = data.reward_type === 'reset_voucher'
  return {
    name: data.name,
    quota: isResetVoucher ? 0 : parseQuotaFromDollars(data.quota_dollars),
    reward_type: data.reward_type,
    reset_plan_id: isResetVoucher ? data.reset_plan_id : 0,
    reset_voucher_expires_at:
      isResetVoucher && data.reset_voucher_expires_at
        ? Math.floor(data.reset_voucher_expires_at.getTime() / 1000)
        : 0,
    expired_time: data.expired_time
      ? Math.floor(data.expired_time.getTime() / 1000)
      : 0,
    count: data.count || 1,
  }
}

export function transformRedemptionToFormDefaults(
  redemption: Redemption
): RedemptionFormValues {
  return {
    name: redemption.name,
    quota_dollars: quotaUnitsToEditableAmount(redemption.quota),
    reward_type: redemption.reward_type ?? 'quota',
    reset_plan_id: redemption.reset_plan_id ?? 0,
    reset_voucher_expires_at:
      (redemption.reset_voucher_expires_at ?? 0) > 0
        ? new Date((redemption.reset_voucher_expires_at ?? 0) * 1000)
        : undefined,
    expired_time:
      redemption.expired_time > 0
        ? new Date(redemption.expired_time * 1000)
        : undefined,
    count: 1,
  }
}
