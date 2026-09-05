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
import { useQuery } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Skeleton } from '@/components/ui/skeleton'
import { formatLogQuota } from '@/lib/format'
import { cn } from '@/lib/utils'

import { getLogStats, getUserLogStats } from '../api'
import { DEFAULT_LOG_STATS } from '../constants'
import { buildApiParams, getDefaultTimeRange } from '../lib/utils'
import type { LogStatistics } from '../types'
import { useLogsViewScope, useUsageLogsContext } from './usage-logs-provider'

const route = getRouteApi('/_authenticated/usage-logs/$section')

function StatBadge(props: {
  label: string
  value: string | number
  accent: string
}) {
  return (
    <span className='border-border/60 bg-muted/25 inline-flex h-7 items-center gap-2 rounded-md border px-2.5 text-xs shadow-xs'>
      <span className={cn('h-3.5 w-0.5 rounded-full', props.accent)} />
      <span className='text-muted-foreground'>{props.label}</span>
      <span className='text-foreground/85 font-mono font-semibold tabular-nums'>
        {props.value}
      </span>
    </span>
  )
}

type TrendBucket = LogStatistics & { start: number; end: number }

function UsageTrend(props: {
  buckets: TrendBucket[]
  loading: boolean
  error: boolean
  sensitiveVisible: boolean
  onRetry: () => void
  onSelect: (bucket: TrendBucket) => void
}) {
  const { t } = useTranslation()
  const maxMetric = Math.max(
    1,
    ...props.buckets.map((bucket) =>
      props.sensitiveVisible ? bucket.quota : bucket.rpm
    )
  )

  if (props.loading) {
    return <Skeleton className='h-16 w-full min-w-[220px] rounded-md' />
  }
  if (props.error) {
    return (
      <div className='text-muted-foreground flex items-center gap-2 text-xs'>
        <span>{t('Unable to load usage trend')}</span>
        <button
          type='button'
          className='text-foreground underline underline-offset-2'
          onClick={props.onRetry}
        >
          {t('Retry')}
        </button>
      </div>
    )
  }
  if (
    !props.buckets.some(
      (bucket) => bucket.quota > 0 || bucket.rpm > 0 || bucket.tpm > 0
    )
  ) {
    return (
      <span className='text-muted-foreground text-xs'>
        {t('No usage trend data in this range')}
      </span>
    )
  }

  const formatBucket = (timestamp: number) =>
    new Intl.DateTimeFormat(undefined, {
      month: 'short',
      day: 'numeric',
      hour: 'numeric',
    }).format(new Date(timestamp))

  return (
    <div className='min-w-[220px] flex-1'>
      <div
        className='flex h-16 items-end gap-1'
        role='img'
        aria-label={t('Usage trend for the selected time range')}
      >
        {props.buckets.map((bucket) => {
          const value = props.sensitiveVisible ? bucket.quota : bucket.rpm
          const height = value > 0 ? Math.max(10, (value / maxMetric) * 100) : 4
          return (
            <button
              key={`${bucket.start}-${bucket.end}`}
              type='button'
              className='bg-primary/55 hover:bg-primary/80 focus-visible:ring-ring min-w-0 flex-1 rounded-t-sm transition-[height,background-color] focus-visible:ring-2 focus-visible:outline-none motion-reduce:transition-none'
              style={{ height: `${height}%` }}
              title={`${formatBucket(bucket.start)} · ${props.sensitiveVisible ? formatLogQuota(bucket.quota) : `${bucket.rpm} RPM`}`}
              aria-label={`${formatBucket(bucket.start)}: ${props.sensitiveVisible ? formatLogQuota(bucket.quota) : `${bucket.rpm} RPM`}`}
              onClick={() => props.onSelect(bucket)}
            />
          )
        })}
      </div>
      <p className='text-muted-foreground mt-1 text-[11px]'>
        {t('Click a bar to filter logs to that time window.')}
      </p>
    </div>
  )
}

export function CommonLogsStats() {
  const { t } = useTranslation()
  const { isAdminView: isAdmin } = useLogsViewScope()
  const searchParams = route.useSearch()
  const { sensitiveVisible } = useUsageLogsContext()
  const navigate = route.useNavigate()

  const { data: stats, isLoading } = useQuery({
    queryKey: ['usage-logs-stats', isAdmin, searchParams],
    queryFn: async () => {
      const params = buildApiParams({
        page: 1,
        pageSize: 1,
        searchParams,
        columnFilters: [],
        isAdmin,
      })

      const result = isAdmin
        ? await getLogStats(params)
        : await getUserLogStats(params)

      return result.success
        ? result.data || DEFAULT_LOG_STATS
        : DEFAULT_LOG_STATS
    },
    placeholderData: (previousData) => previousData,
  })

  const trendBuckets = useMemo(() => {
    const fallback = getDefaultTimeRange()
    const start = Number(searchParams.startTime) || fallback.start.getTime()
    const end = Number(searchParams.endTime) || Date.now()
    const safeEnd = end > start ? end : start + 60 * 60 * 1000
    const bucketCount = 12
    const size = (safeEnd - start) / bucketCount
    return Array.from({ length: bucketCount }, (_, index) => ({
      start: Math.round(start + index * size),
      end: Math.round(
        index === bucketCount - 1 ? safeEnd : start + (index + 1) * size
      ),
    }))
  }, [searchParams.endTime, searchParams.startTime])

  const trendQuery = useQuery({
    queryKey: ['usage-logs-trend', isAdmin, searchParams],
    queryFn: async () => {
      const results = await Promise.all(
        trendBuckets.map(async (bucket) => {
          const params = buildApiParams({
            page: 1,
            pageSize: 1,
            searchParams: {
              ...searchParams,
              startTime: bucket.start,
              endTime: bucket.end,
            },
            columnFilters: [],
            isAdmin,
          })
          const result = isAdmin
            ? await getLogStats(params)
            : await getUserLogStats(params)
          return result.success
            ? { ...bucket, ...(result.data || DEFAULT_LOG_STATS) }
            : null
        })
      )
      if (results.every((result) => result === null)) {
        throw new Error('Unable to load usage trend')
      }
      return results.filter((result): result is TrendBucket => result !== null)
    },
    placeholderData: (previousData) => previousData,
  })

  if (isLoading) {
    return (
      <div className='flex items-center gap-2'>
        <Skeleton className='h-7 w-[150px] rounded-md' />
        <Skeleton className='h-7 w-[100px] rounded-md' />
        <Skeleton className='h-7 w-[120px] rounded-md' />
      </div>
    )
  }

  return (
    <div className='flex w-full flex-wrap items-center gap-2'>
      <div className='flex flex-wrap items-center gap-2'>
        <StatBadge
          label={t('Usage')}
          value={sensitiveVisible ? formatLogQuota(stats?.quota || 0) : '••••'}
          accent='console-stat-accent-info'
        />
        <StatBadge
          label={t('RPM')}
          value={stats?.rpm || 0}
          accent='console-stat-accent-danger'
        />
        <StatBadge
          label={t('TPM')}
          value={stats?.tpm || 0}
          accent='console-stat-accent-neutral'
        />
      </div>
      <UsageTrend
        buckets={trendQuery.data ?? []}
        loading={trendQuery.isLoading}
        error={trendQuery.isError}
        sensitiveVisible={sensitiveVisible}
        onRetry={() => void trendQuery.refetch()}
        onSelect={(bucket) => {
          void navigate({
            to: '/usage-logs/$section',
            params: { section: 'common' },
            search: (previous) => ({
              ...previous,
              startTime: bucket.start,
              endTime: bucket.end,
              page: 1,
            }),
          })
        }}
      />
    </div>
  )
}
