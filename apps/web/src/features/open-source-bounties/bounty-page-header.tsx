/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { ArrowUpRight, ChevronDown, Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

type BountyPageHeaderProps = {
  onCreate: () => void
  /** The server supplies basis points. Missing data must not imply a zero fee. */
  feeRate?: number
  feeError: boolean
  isSuperAdmin: boolean
}

export function BountyPageHeader({
  onCreate,
  feeRate,
  feeError,
  isSuperAdmin,
}: BountyPageHeaderProps) {
  const { t } = useTranslation()
  const feeKnown = Number.isFinite(feeRate) && (feeRate ?? -1) >= 0

  return (
    <header className='space-y-4 sm:space-y-5'>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <h1 className='text-xl font-bold tracking-tight sm:text-2xl'>
          {t('Open-source bounties')}
        </h1>
        <Button onClick={onCreate} className='min-h-11'>
          <Plus aria-hidden='true' data-icon='inline-start' />
          {t('Create bounty')}
        </Button>
      </div>
      <div className='flex flex-wrap items-start justify-between gap-x-6 gap-y-2'>
        <p className='text-muted-foreground max-w-2xl text-sm leading-relaxed'>
          {t(
            'Publish real bug-fix challenges, accept work, verify the fix, and transfer rewards from escrow.'
          )}
        </p>
        <Button
          variant='ghost'
          className='-ml-2 min-h-11 sm:ml-0'
          render={<a href='/tool-market' />}
        >
          MCP · {t('Tool market')}
          <ArrowUpRight aria-hidden='true' data-icon='inline-end' />
        </Button>
      </div>
      <details className='bounty-funding-details group/funding'>
        <summary className='flex min-h-11 cursor-pointer list-none flex-wrap items-center gap-x-3 gap-y-1 py-2 text-sm [&::-webkit-details-marker]:hidden'>
          <span className='font-medium'>{t('How the money moves')}</span>
          <span className='text-muted-foreground ml-auto text-xs tabular-nums'>
            {feeKnown
              ? t('Public platform fee: {{rate}}%', {
                  rate: ((feeRate ?? 0) / 100).toFixed(2),
                })
              : t(feeError ? 'Unavailable' : 'Loading')}
          </span>
          <ChevronDown
            aria-hidden='true'
            className='size-4 shrink-0 transition-transform group-open/funding:rotate-180 motion-reduce:transition-none'
          />
        </summary>
        <div className='max-w-3xl space-y-3 pt-2 pb-4 text-sm leading-relaxed'>
          <p className='font-medium'>
            {t('Every publisher pays from their own balance')}
          </p>
          <p className='text-muted-foreground'>
            {t(
              'Publishing deducts the gross total from your balance. After the platform fee, the rest is held in escrow for the contributor who fixes it.'
            )}
          </p>
          <p className='text-muted-foreground'>
            {t(
              'The platform fee is credited to the super administrator account and funds AI customer-service token costs. Publishers and contributors settle directly; administrators intervene only in disputes.'
            )}
          </p>
          {isSuperAdmin && (
            <Button
              variant='outline'
              size='sm'
              render={<a href='/system-settings/billing/quota' />}
            >
              {t('Fee settings')}
            </Button>
          )}
        </div>
      </details>
    </header>
  )
}
