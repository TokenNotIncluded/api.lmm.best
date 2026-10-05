/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

/** The balance and top-up link stay visible at compact viewport widths. */
export function AccountBalanceBadge({
  className,
  compactMobile = false,
}: {
  className?: string
  compactMobile?: boolean
}) {
  const { t } = useTranslation()
  const { formatQuota } = useWalletCurrency()
  const quota = useAuthStore((state) => state.auth.user?.quota ?? 0)
  const balance = formatQuota(quota)

  return (
    <>
      {compactMobile && (
        <Link
          to='/wallet'
          className='border-border/70 bg-muted/40 hover:bg-accent focus-visible:ring-ring/50 inline-flex h-11 max-w-20 min-w-11 shrink-0 items-center justify-center rounded-lg border px-2 text-sm font-medium tabular-nums outline-none focus-visible:ring-[3px] sm:hidden'
          aria-label={`${t('Balance')}: ${formatQuota(quota)} · ${t('Top up')}`}
          title={`${t('Balance')}: ${formatQuota(quota)} · ${t('Top up')}`}
          data-testid='mobile-account-balance'
        >
          <span className='min-w-0 truncate'>{balance}</span>
        </Link>
      )}
      <div
        className={cn(
          'border-border/70 bg-muted/40 inline-flex h-11 min-w-0 shrink-0 items-center rounded-lg border text-xs font-medium sm:h-8',
          compactMobile && 'hidden sm:inline-flex',
          className
        )}
        data-testid='account-balance-badge'
      >
        <span className='text-muted-foreground ps-2 sm:ps-2.5'>
          {t('Balance')}
        </span>
        <Tooltip>
          <TooltipTrigger
            render={
              <button
                type='button'
                className='text-muted-foreground hover:text-foreground focus-visible:ring-ring/50 -mt-1 inline-flex size-5 items-center justify-center rounded-full text-[10px] font-semibold outline-none focus-visible:ring-[3px]'
                aria-label={t('Wallet balance')}
              />
            }
          >
            <sup aria-hidden='true'>?</sup>
          </TooltipTrigger>
          <TooltipContent side='bottom' className='max-w-72 leading-5'>
            {t(
              'Your balance is stored in credits. Display currency does not change the amount charged at checkout.'
            )}
          </TooltipContent>
        </Tooltip>
        <Link
          to='/wallet'
          className='hover:text-accent-foreground focus-visible:ring-ring/50 max-w-24 truncate px-1 tabular-nums outline-none focus-visible:ring-[3px]'
          aria-label={`${t('Balance')}: ${balance}`}
        >
          {balance}
        </Link>
        <Link
          to='/wallet'
          className='border-border hover:bg-accent hover:text-accent-foreground focus-visible:ring-ring/50 inline-flex h-full shrink-0 items-center border-s px-2 font-semibold outline-none focus-visible:ring-[3px] sm:px-2.5'
          aria-label={t('Top up')}
        >
          {t('Top up')}
        </Link>
      </div>
    </>
  )
}
