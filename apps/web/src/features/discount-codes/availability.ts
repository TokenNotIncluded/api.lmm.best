/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import {
  CalendarClock,
  CircleCheck,
  CircleSlash,
  Clock,
  type LucideIcon,
} from 'lucide-react'

import type { DiscountCode } from './types'

export const DISCOUNT_CODE_ENABLED_STATUS = 1

export type DiscountCodeAvailability =
  | 'active'
  | 'disabled'
  | 'not_started'
  | 'expired'

export function getDiscountCodeAvailability(
  code: Pick<DiscountCode, 'status' | 'starts_time' | 'expired_time'>,
  now = Math.floor(Date.now() / 1000)
): DiscountCodeAvailability {
  if (code.status !== DISCOUNT_CODE_ENABLED_STATUS) return 'disabled'
  if (code.starts_time > now) return 'not_started'
  if (code.expired_time > 0 && code.expired_time < now) return 'expired'
  return 'active'
}

/**
 * Icon + label per availability so status is never carried by color alone.
 * `labelKey` is an i18n key; call `t(config.labelKey)` in components.
 */
export const DISCOUNT_CODE_AVAILABILITY_CONFIG: Record<
  DiscountCodeAvailability,
  {
    labelKey: string
    variant: 'success' | 'neutral' | 'info' | 'warning'
    icon: LucideIcon
  }
> = {
  active: { labelKey: 'Active', variant: 'success', icon: CircleCheck },
  not_started: {
    labelKey: 'Not Started',
    variant: 'info',
    icon: CalendarClock,
  },
  expired: { labelKey: 'Expired', variant: 'warning', icon: Clock },
  disabled: { labelKey: 'Disabled', variant: 'neutral', icon: CircleSlash },
}

/**
 * Zero is the server's explicit unlimited sentinel. Reject blank, fractional,
 * and unsafe values before sending a value to the int64-backed API.
 */
export function parseDiscountCodeMaxUses(value: string): number | undefined {
  const normalized = value.trim()
  if (!/^\d+$/.test(normalized)) return undefined

  const maxUses = Number(normalized)
  return Number.isSafeInteger(maxUses) ? maxUses : undefined
}
