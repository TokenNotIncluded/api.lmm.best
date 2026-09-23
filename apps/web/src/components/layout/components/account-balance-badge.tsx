/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { formatQuota } from '@/lib/format'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

/** The balance and top-up link stay visible at compact viewport widths. */
export function AccountBalanceBadge({ className }: { className?: string }) {
  const { t } = useTranslation()
  const quota = useAuthStore((state) => state.auth.user?.quota ?? 0)
  const balance = formatQuota(quota)

  return (
    <Link
      to='/wallet'
      className={cn(
        'border-border/70 bg-muted/40 hover:bg-accent hover:text-accent-foreground focus-visible:ring-ring/50 inline-flex h-11 min-w-0 shrink-0 items-center gap-1.5 rounded-lg border px-2 text-xs font-medium outline-none focus-visible:ring-[3px] sm:h-8 sm:px-2.5',
        className
      )}
      aria-label={`${t('Balance')}: ${balance} · ${t('Top up')}`}
      title={`${t('Balance')}: ${balance} · ${t('Top up')}`}
      data-testid='account-balance-badge'
    >
      <span className='max-w-24 truncate tabular-nums' aria-hidden='true'>
        {balance}
      </span>
      <span
        className='border-border shrink-0 border-s ps-1.5 font-semibold'
        aria-hidden='true'
      >
        {t('Top up')}
      </span>
    </Link>
  )
}
