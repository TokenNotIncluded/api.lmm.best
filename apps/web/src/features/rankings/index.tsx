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
import { useNavigate, useSearch } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { PageTransition } from '@/components/page-transition'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { ForgePublicShell } from '@/features/forge/forge-public-shell'

import {
  MarketShareSection,
  ModelsSection,
  PulseSection,
  RankingsHero,
  UserUsageLeaderboard,
} from './components'
import { useRankings } from './hooks/use-rankings'
import { useUserUsageRankings } from './hooks/use-user-usage-rankings'
import type { RankingPeriod } from './types'

const VALID_PERIODS = new Set<RankingPeriod>(['today', 'week', 'month', 'year'])

export function Rankings() {
  const { t } = useTranslation()
  const search = useSearch({ from: '/rankings/' })
  const navigate = useNavigate()
  const [userLeaderboardOpen, setUserLeaderboardOpen] = useState(false)

  const period: RankingPeriod = VALID_PERIODS.has(
    search.period as RankingPeriod
  )
    ? (search.period as RankingPeriod)
    : 'week'

  const rankingsQuery = useRankings(period)
  const userUsageRankingsQuery = useUserUsageRankings(
    period,
    userLeaderboardOpen
  )
  const snapshot = rankingsQuery.data?.data

  const handlePeriodChange = (next: RankingPeriod) => {
    navigate({
      to: '/rankings',
      search: (prev) => ({ ...prev, period: next }),
    })
  }

  return (
    <ForgePublicShell>
      <main className='mx-auto w-full max-w-7xl px-5 pt-12 pb-20 md:px-10 md:pt-16'>
        <PageTransition className='space-y-10'>
          <RankingsHero period={period} onPeriodChange={handlePeriodChange} />

          <UserUsageLeaderboard
            data={userUsageRankingsQuery.data?.data}
            isLoading={userUsageRankingsQuery.isLoading}
            error={userUsageRankingsQuery.error}
            open={userLeaderboardOpen}
            onOpenChange={setUserLeaderboardOpen}
          />

          {rankingsQuery.isLoading ? (
            <RankingsLoading />
          ) : !snapshot ? (
            <RankingsError
              message={
                rankingsQuery.error instanceof Error
                  ? rankingsQuery.error.message
                  : t('Unable to load rankings data')
              }
              onRetry={() => void rankingsQuery.refetch()}
            />
          ) : (
            <>
              <ModelsSection
                history={snapshot.models_history}
                rows={snapshot.models}
                period={period}
              />

              <MarketShareSection
                history={snapshot.vendor_share_history}
                rows={snapshot.vendors}
                period={period}
              />

              <PulseSection
                movers={snapshot.top_movers}
                droppers={snapshot.top_droppers}
              />
            </>
          )}
        </PageTransition>
      </main>
    </ForgePublicShell>
  )
}

function RankingsLoading() {
  return (
    <div className='space-y-6'>
      <Skeleton className='h-[420px] w-full rounded-xl' />
      <Skeleton className='h-[360px] w-full rounded-xl' />
      <Skeleton className='h-[180px] w-full rounded-xl' />
    </div>
  )
}

function RankingsError(props: { message: string; onRetry: () => void }) {
  const { t } = useTranslation()
  return (
    <div className='border-foreground/20 border-y px-6 py-12 text-center'>
      <h2 className='text-foreground text-base font-semibold'>
        {t('Unable to load rankings')}
      </h2>
      <p className='text-muted-foreground mx-auto mt-2 max-w-md text-sm'>
        {props.message}
      </p>
      <Button className='mt-5' variant='outline' onClick={props.onRetry}>
        {t('Retry')}
      </Button>
    </div>
  )
}
