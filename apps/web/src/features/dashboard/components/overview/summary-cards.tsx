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
import { Skeleton } from '@/components/ui/skeleton'
import { getUserQuotaDates } from '@/features/dashboard/api'
import { useSummaryCardsConfig } from '@/features/dashboard/hooks/use-dashboard-config'
import { useStatus } from '@/hooks/use-status'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'
import { normalizedUserUsage } from '@/lib/cumulative-user-usage'
import { formatNumber } from '@/lib/format'
import { computeTimeRange } from '@/lib/time'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { BalanceCurrencySwitch } from './balance-currency-switch'

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
  const {
    formatQuota,
    formatUserUsage,
    label: currencyLabel,
  } = useWalletCurrency()
  const user = useAuthStore((state) => state.auth.user)
  const { loading } = useStatus()

  const summaryTimeRange = useMemo(() => computeTimeRange(1), [])
  const remainQuota = Number(user?.quota ?? 0)
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
    usedDisplay: formatUserUsage(user),
    requestCountDisplay: formatNumber(requestCount),
  }

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
    currencyLabel,
    usedCurrencyLabel:
      normalizedUserUsage(user) === null ? t('Credits') : currencyLabel,
    currencyEnabled: true,
  })

  return (
    <section className='overview-summary' aria-label={t('Usage at a glance')}>
      <div className='overview-balance'>
        <div className='overview-balance-heading'>
          <h3 className='text-muted-foreground text-sm font-medium'>
            {t('Credit remaining')}
          </h3>
          {usageReady || remainQuota <= 0 ? (
            <span className='overview-health'>
              <span
                className={cn('size-1.5 rounded-full', healthCfg.dotClass)}
                aria-hidden='true'
              />
              {t(healthCfg.labelKey)}
            </span>
          ) : null}
        </div>
        <BalanceCurrencySwitch />
        <div
          className='overview-balance-value'
          aria-live='polite'
          aria-atomic='true'
        >
          {loading ? (
            <Skeleton className='h-12 w-48 max-w-full' />
          ) : (
            <>
              <span>{formatQuota(remainQuota, { showSymbol: false })}</span>{' '}
              <span className='overview-unit'>{currencyLabel}</span>
            </>
          )}
        </div>
        <div className='overview-balance-footer'>
          <dl className='min-w-0 text-sm'>
            <dt className='text-muted-foreground text-xs'>{t('Runway')}</dt>
            <dd
              className={cn(
                'mt-1 font-medium tabular-nums',
                healthLevel === 'critical' && 'text-destructive',
                usageReady && healthLevel === 'caution' && 'text-warning'
              )}
            >
              {runwayDisplay}
            </dd>
          </dl>
          <Button
            className='min-h-11 gap-4 px-5'
            render={<Link to='/wallet' />}
          >
            {t('Wallet')}
            <ArrowRight data-icon='inline-end' aria-hidden='true' />
          </Button>
        </div>
      </div>
      <div className='overview-usage'>
        <h3 className='mb-5 text-sm font-semibold'>{t('Usage at a glance')}</h3>
        <dl className='overview-metrics'>
          {items.map((item) => (
            <div className='overview-metric' key={item.key}>
              <dt className='text-muted-foreground text-xs'>{item.title}</dt>
              <dd className='overview-metric-value'>
                {loading ||
                (item.key === 'todayUsage' && usageTrendQuery.isLoading) ? (
                  <Skeleton className='h-7 w-28 max-w-full' />
                ) : (
                  item.value
                )}
              </dd>
              <dd className='sr-only'>{item.description}</dd>
            </div>
          ))}
        </dl>
        {usageFailed && (
          <div
            role='status'
            className='text-muted-foreground mt-4 flex flex-wrap items-center gap-2 text-sm'
          >
            <span>{t('Failed to load data')}</span>
            <Button
              type='button'
              variant='ghost'
              className='min-h-11'
              disabled={usageTrendQuery.isFetching}
              onClick={() => void usageTrendQuery.refetch()}
            >
              {t('Retry')}
            </Button>
          </div>
        )}
      </div>
    </section>
  )
}
