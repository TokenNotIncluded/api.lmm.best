/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { ArrowUpRight, Share2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { TitledCard } from '@/components/ui/titled-card'

/** Always available: disabled check-in and an empty gift list must not make Rewards blank. */
export function ReferralOverviewCard() {
  const { t } = useTranslation()
  return (
    <TitledCard
      title={t('Referral Program')}
      icon={<Share2 />}
      iconTone='chart-3'
    >
      <div className='space-y-4'>
        <p className='text-muted-foreground text-sm leading-relaxed'>
          {t(
            'A small registration advance can be enabled. The tail reward requires a qualifying first cash payment. Confirmed abuse or a full refund can revoke rewards. Reward debt never deducts purchased balance.'
          )}
        </p>
        <Button
          variant='outline'
          className='min-h-11'
          render={<a href='/wallet' />}
        >
          {t('Wallet')}
          <ArrowUpRight aria-hidden='true' data-icon='inline-end' />
        </Button>
      </div>
    </TitledCard>
  )
}
