/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'

import {
  DISCOUNT_CODE_AVAILABILITY_CONFIG,
  getDiscountCodeAvailability,
} from '../availability'
import type { DiscountCode } from '../types'

interface DiscountCodeStatusBadgeProps {
  code: Pick<DiscountCode, 'status' | 'starts_time' | 'expired_time'>
  className?: string
}

/**
 * Status is derived from the same availability helper the server-side
 * checkout follows, and always renders icon + text so the state is never
 * carried by color alone.
 */
export function DiscountCodeStatusBadge({
  code,
  className,
}: DiscountCodeStatusBadgeProps) {
  const { t } = useTranslation()
  const config =
    DISCOUNT_CODE_AVAILABILITY_CONFIG[getDiscountCodeAvailability(code)]

  return (
    <StatusBadge
      label={t(config.labelKey)}
      variant={config.variant}
      icon={config.icon}
      copyable={false}
      className={className}
    />
  )
}
