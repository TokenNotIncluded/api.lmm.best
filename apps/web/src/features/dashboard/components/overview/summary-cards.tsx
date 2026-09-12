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
import { Link } from '@tanstack/react-router'
import { ArrowRight } from 'lucide-react'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { getUserQuotaDates } from '@/features/dashboard/api'
import { useSummaryCardsConfig } from '@/features/dashboard/hooks/use-dashboard-config'
import type { QuotaDataItem } from '@/features/dashboard/types'
import { useStatus } from '@/hooks/use-status'
import { getCurrencyLabel, isCurrencyDisplayEnabled } from '@/lib/currency'
import { formatNumber, formatQuota } from '@/lib/format'
import { computeTimeRange } from '@/lib/time'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { StatCard } from '../ui/stat-card'

const SUMMARY_SPARKLINE_BUCKETS = 12

type SummarySparklineKey = 'balance' | 'usage' | 'requests'

function getBucketIndex(
  timestamp: number,
  start: number,
  end: number,
  bucketCount: number
): number {
  if (end <= start) return 0
  const ratio = (timestamp - start) / (end - start)
  return Math.min(bucketCount - 1, Math.max(0, Math.floor(ratio * bucketCount)))
}

function buildSummarySparklines(
  data: QuotaDataItem[],
  currentBalance: number,
  start: number,
  end: number
): Record<SummarySparklineKey, number[]> {
  const usage = Array.from({ length: SUMMARY_SPARKLINE_BUCKETS }, () => 0)
  const requests = Array.from({ length: SUMMARY_SPARKLINE_BUCKETS }, () => 0)

  for (const item of data) {
    const timestamp = Number(item.created_at) || start
    const index = getBucketIndex(
      timestamp,
      start,
      end,
      SUMMARY_SPARKLINE_BUCKETS
    )
    usage[index] += Number(item.quota) || 0
    requests[index] += Number(item.count) || 0
  }

  let balance = currentBalance
  const balanceTrend = Array.from(
    { length: SUMMARY_SPARKLINE_BUCKETS },
    () => 0
  )

  for (let index = SUMMARY_SPARKLINE_BUCKETS - 1; index >= 0; index--) {
    balanceTrend[index] = Math.max(0, balance)
    balance += usage[index]
  }

  return {
    balance: balanceTrend,
    usage,
    requests,
  }
}

function getSummarySparkline(
  key: string,
  sparklineData: Record<SummarySparklineKey, number[]>
): number[] | undefined {
  if (key === 'usage') return sparklineData.usage
  if (key === 'requests') return sparklineData.requests
  return undefined
}

function getRunwayDays(
  remainQuota: number,
  recentUsage: number
): number | null {
  if (remainQuota <= 0 || recentUsage <= 0) return null
  const days = remainQuota / recentUsage
  if (!Number.isFinite(days)) return null
  return days
}

type HealthLevel = 'healthy' | 'caution' | 'critical'

function getHealthLevel(remainQuota: number, recentUsage: number): HealthLevel {
  if (remainQuota <= 0) return 'critical'
  const days = getRunwayDays(remainQuota, recentUsage)
  if (days !== null && days < 3) return 'caution'
  return 'healthy'
}

const HEALTH_CONFIG: Record<
  HealthLevel,
  { dotClass: string; labelKey: string }
> = {
  healthy: {
    dotClass: 'bg-success',
    labelKey: 'Healthy',
  },
  caution: {
    dotClass: 'bg-warning',
    labelKey: 'Low balance',
  },
  critical: {
    dotClass: 'bg-destructive',
    labelKey: 'Balance depleted',
  },
}

export function SummaryCards() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const { status, loading } = useStatus()

  const summaryTimeRange = useMemo(() => computeTimeRange(1), [])
  const remainQuota = Number(user?.quota ?? 0)
  const usedQuota = Number(user?.used_quota ?? 0)
  const requestCount = Number(user?.request_count ?? 0)

  const usageTrendQuery = useQuery({
    queryKey: [
      'dashboard',
      'overview',
      'summary-sparklines',
      user?.id,
      summaryTimeRange.start_timestamp,
      summaryTimeRange.end_timestamp,
    ],
    queryFn: async () =>
      getUserQuotaDates({
        start_timestamp: summaryTimeRange.start_timestamp,
        end_timestamp: summaryTimeRange.end_timestamp,
        default_time: 'hour',
      }),
    staleTime: 60 * 1000,
    enabled: Boolean(user?.id),
  })

  // Formatting follows the current language and currency configuration on every render.
  const summaryValues = {
    usedDisplay: formatQuota(usedQuota),
    requestCountDisplay: formatNumber(requestCount),
  }

  const currencyEnabledFromStore = isCurrencyDisplayEnabled()
  const statusCurrencyFlag =
    typeof status?.display_in_currency === 'boolean'
      ? Boolean(status.display_in_currency)
      : undefined
  const currencyEnabled =
    statusCurrencyFlag !== undefined
      ? statusCurrencyFlag
      : currencyEnabledFromStore
  const currencyLabel = currencyEnabled ? getCurrencyLabel() : 'Tokens'

  const sparklineData = useMemo(
    () =>
      buildSummarySparklines(
        usageTrendQuery.data?.data ?? [],
        remainQuota,
        summaryTimeRange.start_timestamp,
        summaryTimeRange.end_timestamp
      ),
    [
      remainQuota,
      summaryTimeRange.end_timestamp,
      summaryTimeRange.start_timestamp,
      usageTrendQuery.data?.data,
    ]
  )

  const recentUsage = useMemo(
    () =>
      (usageTrendQuery.data?.data ?? []).reduce(
        (total, item) => total + (Number(item.quota) || 0),
        0
      ),
    [usageTrendQuery.data?.data]
  )

  const usageReady =
    usageTrendQuery.isSuccess && usageTrendQuery.data.success === true
  const usageFailed =
    usageTrendQuery.isError || usageTrendQuery.data?.success === false
  const healthLevel = getHealthLevel(remainQuota, recentUsage)
  const healthCfg = HEALTH_CONFIG[healthLevel]
  const runwayDays = getRunwayDays(remainQuota, recentUsage)

  const todayUsageDisplay = usageReady ? formatQuota(recentUsage) : '—'
  let runwayDisplay: string
  if (!usageReady) {
    runwayDisplay = t('Unknown')
  } else if (runwayDays !== null) {
    if (runwayDays < 1) {
      runwayDisplay = t('Less than 1 day left')
    } else if (runwayDays > 999) {
      runwayDisplay = `999+ ${t('days')}`
    } else {
      runwayDisplay = `~${formatNumber(Math.floor(runwayDays))} ${t('days')}`
    }
  } else if (remainQuota <= 0) {
    runwayDisplay = t('Balance depleted')
  } else {
    runwayDisplay = t('No recent usage')
  }

  const items = useSummaryCardsConfig({
    ...summaryValues,
    todayUsageDisplay,
    currencyEnabled,
    currencyLabel,
  }).map((config, index) => {
    const tones = ['accent-1', 'accent-2', 'accent-3'] as const

    return {
      key: config.key,
      title: config.title,
      value: config.value,
      desc: config.description,
      icon: config.icon,
      tone: tones[index] ?? 'accent-3',
      sparkline:
        usageReady && (usageTrendQuery.data?.data.length ?? 0) > 0
          ? config.key === 'todayUsage'
            ? sparklineData.usage
            : getSummarySparkline(config.key, sparklineData)
          : undefined,
      sparklineVariant: 'line' as const,
    }
  })

  return (
    <section
      className='border-border border-b pb-8'
      aria-label={t('Usage at a glance')}
    >
      <div className='grid gap-8 lg:grid-cols-[minmax(0,1fr)_18rem]'>
        <div className='order-2 min-w-0 lg:order-1'>
          <h3 className='mb-6 text-base font-semibold'>
            {t('Usage at a glance')}
          </h3>
          <div className='grid grid-cols-1 gap-4 sm:grid-cols-3 sm:gap-8'>
            {items.map((it) => (
              <StatCard
                key={it.key}
                title={it.title}
                value={it.value}
                description={it.desc}
                icon={it.icon}
                tone={it.tone}
                sparkline={it.sparkline}
                sparklineVariant={it.sparklineVariant}
                loading={
                  loading ||
                  (it.key === 'todayUsage' && usageTrendQuery.isLoading)
                }
                error={it.key === 'todayUsage' && usageFailed}
                plain
              />
            ))}
          </div>
          {usageFailed && (
            <div
              role='status'
              className='text-muted-foreground mt-4 flex flex-wrap items-center gap-2 text-sm'
            >
              <span>{t('Failed to load data')}</span>
              <Button
                type='button'
                variant='ghost'
                size='sm'
                disabled={usageTrendQuery.isFetching}
                onClick={() => void usageTrendQuery.refetch()}
              >
                {t('Retry')}
              </Button>
            </div>
          )}
        </div>
        <div className='order-1 flex min-w-0 flex-col gap-4 lg:order-2'>
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <span className='text-sm font-medium'>{t('Credit remaining')}</span>
            {usageReady || remainQuota <= 0 ? (
              <span className='text-muted-foreground inline-flex items-center gap-2 text-xs'>
                <span
                  className={cn('size-1.5 rounded-full', healthCfg.dotClass)}
                  aria-hidden='true'
                />
                {t(healthCfg.labelKey)}
              </span>
            ) : null}
          </div>
          <div className='text-2xl font-semibold break-words tabular-nums'>
            {formatQuota(remainQuota)}
          </div>
          <dl className='flex flex-wrap items-baseline justify-between gap-2 text-sm'>
            <dt className='text-muted-foreground'>{t('Runway')}</dt>
            <dd
              className={cn(
                'tabular-nums',
                healthLevel === 'critical' && 'text-destructive',
                usageReady && healthLevel === 'caution' && 'text-warning'
              )}
            >
              {runwayDisplay}
            </dd>
          </dl>
          <Button
            className='min-h-11 justify-between'
            render={<Link to='/wallet' />}
          >
            <span>{t('Wallet')}</span>
            <ArrowRight data-icon='inline-end' />
          </Button>
        </div>
      </div>
    </section>
  )
}
