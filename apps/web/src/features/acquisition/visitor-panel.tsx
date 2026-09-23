/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery } from '@tanstack/react-query'
import { Globe, Info, Users, Waypoints } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { TitledCard } from '@/components/ui/titled-card'
import { api } from '@/lib/api'

import type { AcquisitionRange } from './report-range'

type Visitors = {
  available_from: number
  coverage_complete: boolean
  observed_visitors: number
  channels: { source: string; visitors: number }[]
}
export function AcquisitionVisitorPanel({ from, to }: AcquisitionRange) {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: ['acquisition-visitors', from, to],
    retry: false,
    queryFn: async () => {
      const response = await api.get(
        `/api/admin/acquisition/visitors?from=${from}&to=${to}`
      )
      if (!response.data.success) throw new Error('Visitor data unavailable')
      return response.data.data as Visitors
    },
  })
  const peak = query.data?.channels.reduce(
    (max, row) => Math.max(max, row.visitors),
    0
  )

  return (
    <TitledCard
      title={t('Identifiable browser visitors')}
      description={t(
        'Counts cover consenting browser identifiers, not people. One browser can appear in several channels, so channel counts must not be added together.'
      )}
      icon={<Users aria-hidden='true' />}
      iconTone='chart-3'
      appearance='outlined'
      disableHoverEffect
      contentClassName='space-y-3'
    >
      {query.isError ? (
        <ErrorState
          title={t('We could not load the visitor report.')}
          description={
            query.error instanceof Error ? query.error.message : undefined
          }
          onRetry={() => void query.refetch()}
          className='min-h-[200px]'
        />
      ) : query.isPending ? (
        <LoadingState />
      ) : (
        <>
          {!query.data.coverage_complete && (
            <p
              role='status'
              className='console-status-warning-surface flex items-start gap-2 rounded-lg px-3 py-2 text-sm'
            >
              <Info className='mt-0.5 size-4 shrink-0' aria-hidden='true' />
              <span>
                {t(
                  'Visitor records do not cover this entire period. Counts below include only retained observations.'
                )}
              </span>
            </p>
          )}

          <dl className='bg-muted/40 grid grid-cols-2 gap-px rounded-lg border sm:max-w-md'>
            <div className='flex flex-col gap-1 p-3'>
              <dt className='text-muted-foreground text-xs'>
                {t('Observed browser identifiers')}
              </dt>
              <dd className='font-mono text-lg leading-tight font-semibold tabular-nums'>
                {query.data.observed_visitors}
              </dd>
            </div>
            <div className='flex flex-col gap-1 p-3'>
              <dt className='text-muted-foreground text-xs'>
                {t('Channels seen')}
              </dt>
              <dd className='font-mono text-lg leading-tight font-semibold tabular-nums'>
                {query.data.channels.length}
              </dd>
            </div>
          </dl>

          <p className='text-muted-foreground text-xs'>
            {t('Records available from')}:{' '}
            <time
              dateTime={new Date(
                query.data.available_from * 1000
              ).toISOString()}
            >
              {new Date(query.data.available_from * 1000).toISOString()}
            </time>
          </p>

          {query.data.channels.length === 0 ? (
            <EmptyState
              icon={Globe}
              title={t(
                'No visitor observations in the retained part of this period.'
              )}
              description={t(
                'Consenting browser identifiers appear here as soon as the site reports them.'
              )}
              className='py-8'
            />
          ) : (
            <>
              <h3 className='flex items-center gap-2 text-sm font-medium'>
                <Waypoints className='size-4' aria-hidden='true' />
                {t('Visitors by channel')}
              </h3>
              <ul className='divide-y'>
                {query.data.channels.map((row) => (
                  <li
                    key={row.source}
                    className='flex items-center justify-between gap-3 py-2.5 text-sm'
                  >
                    <span className='min-w-0 truncate'>
                      {row.source === 'unknown'
                        ? t('Direct / unknown source')
                        : row.source}
                    </span>
                    <span className='flex shrink-0 items-center gap-2'>
                      <span
                        className='bg-muted h-1.5 w-16 overflow-hidden rounded-full sm:w-28'
                        aria-hidden='true'
                      >
                        <span
                          className='bg-primary/70 block h-full rounded-full'
                          style={{
                            width: `${peak ? Math.max(4, Math.round((row.visitors / peak) * 100)) : 0}%`,
                          }}
                        />
                      </span>
                      <Badge variant='secondary' className='tabular-nums'>
                        {row.visitors}
                      </Badge>
                    </span>
                  </li>
                ))}
              </ul>
            </>
          )}
        </>
      )}
    </TitledCard>
  )
}
