/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import { ChevronDown, Eye, Trophy } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Skeleton } from '@/components/ui/skeleton'

import { formatShare, formatTokens } from '../lib/format'
import type { UserUsageRankingsSnapshot } from '../types'

type UserUsageLeaderboardProps = {
  data?: UserUsageRankingsSnapshot
  isLoading: boolean
  error?: unknown
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function UserUsageLeaderboard(props: UserUsageLeaderboardProps) {
  const { t } = useTranslation()
  const data = props.data

  return (
    <Collapsible
      open={props.open}
      onOpenChange={props.onOpenChange}
      className='bg-card overflow-hidden rounded-lg border'
    >
      <div className='flex flex-col gap-3 px-5 py-4 sm:flex-row sm:items-end sm:justify-between'>
        <CollapsibleTrigger className='group flex min-w-0 flex-1 items-start justify-between gap-4 text-left'>
          <div className='space-y-1.5'>
            <div className='flex items-center gap-2'>
              <Trophy className='text-muted-foreground h-4 w-4' />
              <h2 className='text-xl font-bold tracking-tight sm:text-2xl'>
                {t('User usage leaderboard')}
              </h2>
            </div>
            <p className='text-muted-foreground max-w-2xl text-sm'>
              {t(
                'See the most active users by token usage. Each person controls whether their name is shown.'
              )}
            </p>
          </div>
          <ChevronDown className='text-muted-foreground mt-1 h-5 w-5 shrink-0 transition-transform group-data-[panel-open]:rotate-180' />
        </CollapsibleTrigger>
        {data && !props.isLoading && !props.error && (
          <div className='text-muted-foreground flex items-center gap-3 text-xs tabular-nums'>
            <span>
              {data.participant_count} {t('participants')}
            </span>
            <span aria-hidden>·</span>
            <span>{t('Updated from live usage data')}</span>
          </div>
        )}
      </div>

      <CollapsibleContent className='px-5 pt-0 pb-5'>
        {props.isLoading ? (
          <div className='divide-border/60 divide-y'>
            {Array.from({ length: 5 }, (_, index) => (
              <div key={index} className='flex items-center gap-3 py-3.5'>
                <Skeleton className='h-4 w-7' />
                <Skeleton className='h-5 flex-1' />
                <Skeleton className='h-5 w-28' />
              </div>
            ))}
          </div>
        ) : props.error || !data ? (
          <p className='text-muted-foreground py-8 text-center text-sm'>
            {t('User usage rankings are unavailable right now.')}
          </p>
        ) : data.users.length === 0 ? (
          <p className='text-muted-foreground py-8 text-center text-sm'>
            {t('No public usage data yet.')}
          </p>
        ) : (
          <div className='divide-border/60 divide-y'>
            {data.users.map((row) => (
              <div
                key={`${row.rank}-${row.anonymous ? 'anonymous' : row.name}`}
                className='hover:bg-muted/30 grid grid-cols-[2.5rem_minmax(0,1fr)_auto] items-center gap-3 px-2 py-3.5 transition-colors sm:grid-cols-[3rem_minmax(0,1fr)_10rem_5rem]'
              >
                <span className='text-muted-foreground font-mono text-xs tabular-nums'>
                  {String(row.rank).padStart(2, '0')}
                </span>
                <div className='min-w-0'>
                  <div className='flex items-center gap-2'>
                    {row.anonymous && (
                      <Eye className='text-muted-foreground h-3.5 w-3.5 shrink-0' />
                    )}
                    <p className='truncate text-sm font-medium'>
                      {row.anonymous ? t('Anonymous users') : row.name}
                    </p>
                  </div>
                </div>
                <div className='text-right'>
                  <p className='font-mono text-sm font-semibold tabular-nums'>
                    {formatTokens(row.total_tokens)}{' '}
                    <span className='text-muted-foreground font-normal'>
                      {t('tokens')}
                    </span>
                  </p>
                  <p className='text-muted-foreground text-xs tabular-nums'>
                    {row.requests.toLocaleString()} {t('requests')}
                  </p>
                </div>
                <span className='text-muted-foreground hidden text-right font-mono text-xs tabular-nums sm:block'>
                  {formatShare(row.share)}
                </span>
              </div>
            ))}
          </div>
        )}
      </CollapsibleContent>
    </Collapsible>
  )
}
