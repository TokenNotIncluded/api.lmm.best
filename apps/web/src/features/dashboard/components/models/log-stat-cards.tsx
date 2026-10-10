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
import { AlertCircle, CalendarDays, RefreshCw } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { IconBadge } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { getUserQuotaDates } from '@/features/dashboard/api'
import { useModelStatCardsConfig } from '@/features/dashboard/hooks/use-dashboard-config'
import {
  buildQueryParams,
  calculateDashboardStats,
  getDefaultDays,
} from '@/features/dashboard/lib'
import { registerDashboardUsageTranslations } from '@/features/dashboard/model-usage-i18n'
import type {
  QuotaDataItem,
  DashboardFilters,
} from '@/features/dashboard/types'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'
import { toIntlLocale } from '@/i18n/languages'
import { formatCompactNumber, formatNumber } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { computeTimeRange } from '@/lib/time'
import { useAuthStore } from '@/stores/auth-store'

interface LogStatCardsProps {
  filters?: DashboardFilters
  onDataUpdate?: (
    data: QuotaDataItem[],
    loading: boolean,
    error?: boolean
  ) => void
}

const MAX_INLINE_STAT_CHARS = 9

function formatStatNumber(value: number, locale: Intl.LocalesArgument) {
  const fullValue = formatNumber(value, locale)
  return {
    displayValue:
      fullValue.length > MAX_INLINE_STAT_CHARS
        ? formatCompactNumber(value, locale)
        : fullValue,
    fullValue,
  }
}

export function LogStatCards({ filters, onDataUpdate }: LogStatCardsProps) {
  const { t, i18n } = useTranslation()
  registerDashboardUsageTranslations(i18n)
  const { formatQuota } = useWalletCurrency()
  const statCardsConfig = useModelStatCardsConfig()
  const userRole = useAuthStore((state) => state.auth.user?.role)
  const isAdmin = Boolean(userRole && userRole >= ROLE.ADMIN)
  const [stats, setStats] = useState<ReturnType<
    typeof calculateDashboardStats
  > | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(false)
  const [refreshVersion, setRefreshVersion] = useState(0)
  const [updatedAt, setUpdatedAt] = useState<Date | null>(null)
  const [timeRangeMinutes, setTimeRangeMinutes] = useState(0)
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)

  useEffect(() => {
    const controller = new AbortController()
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLoading(true)
    setError(false)
    onDataUpdate?.([], true)

    const timeRange = computeTimeRange(
      getDefaultDays(filters?.time_granularity),
      filters?.start_timestamp,
      filters?.end_timestamp
    )
    setTimeRangeMinutes(
      (timeRange.end_timestamp - timeRange.start_timestamp) / 60
    )

    void getUserQuotaDates(
      buildQueryParams(timeRange, filters),
      isAdmin,
      controller.signal
    )
      .then((res) => {
        if (controller.signal.aborted) return
        if (!res?.success) throw new Error('Usage request failed')
        const data = res.data ?? []
        if (!Array.isArray(data)) throw new Error('Invalid usage response')
        setStats(calculateDashboardStats(data))
        setUpdatedAt(new Date())
        onDataUpdate?.(data, false)
      })
      .catch(() => {
        if (controller.signal.aborted) return
        setStats(null)
        setError(true)
        onDataUpdate?.([], false, true)
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false)
      })

    return () => controller.abort()
  }, [filters, isAdmin, onDataUpdate, refreshVersion])

  const adaptedStats = {
    rpm: stats?.totalCount ?? 0,
    quota: stats?.totalQuota ?? 0,
    tpm: stats?.totalTokens ?? 0,
  }
  const start = filters?.start_timestamp
  const end = filters?.end_timestamp
  const rangeLabel =
    start &&
    end &&
    Number.isFinite(start.getTime()) &&
    Number.isFinite(end.getTime()) &&
    start <= end
      ? new Intl.DateTimeFormat(locale, {
          month: 'short',
          day: 'numeric',
          hour: '2-digit',
          minute: '2-digit',
        }).formatRange(start, end)
      : null
  const updatedLabel =
    !error && updatedAt
      ? t('lastUpdated', {
          ns: 'dashboardUsage',
          time: new Intl.DateTimeFormat(locale, {
            hour: '2-digit',
            minute: '2-digit',
          }).format(updatedAt),
        })
      : null

  return (
    <section className='dashboard-stats' aria-busy={loading}>
      <div className='dashboard-stats__toolbar'>
        <div className='dashboard-stats__period'>
          {rangeLabel && (
            <>
              <CalendarDays size={15} aria-hidden='true' />
              <span>{rangeLabel}</span>
            </>
          )}
          {filters?.username && (
            <span className='dashboard-stats__user'>{filters.username}</span>
          )}
        </div>
        <div className='dashboard-stats__refresh'>
          <span role='status'>
            {loading ? t('loading', { ns: 'dashboardUsage' }) : updatedLabel}
          </span>
          <Button
            type='button'
            variant='outline'
            size='sm'
            disabled={loading}
            onClick={() => setRefreshVersion((version) => version + 1)}
          >
            <RefreshCw
              size={14}
              aria-hidden='true'
              className={loading ? 'motion-safe:animate-spin' : undefined}
            />
            {t(error ? 'retry' : 'refresh', { ns: 'dashboardUsage' })}
          </Button>
        </div>
      </div>
      {error && (
        <div className='dashboard-stats__error' role='alert'>
          <AlertCircle size={16} aria-hidden='true' />
          {t('failed', { ns: 'dashboardUsage' })}
        </div>
      )}
      <dl className='dashboard-stats__grid'>
        {statCardsConfig.map((config) => {
          const Icon = config.icon
          const rawValue = config.getValue(adaptedStats, timeRangeMinutes)
          const formatted =
            config.key === 'quota'
              ? {
                  displayValue: formatQuota(rawValue),
                  fullValue: formatQuota(rawValue),
                }
              : formatStatNumber(rawValue, locale)
          return (
            <div
              key={config.key}
              className='dashboard-stat'
              data-primary={config.key === 'quota'}
            >
              <dt>
                <IconBadge tone={config.iconTone} size='stat'>
                  <Icon />
                </IconBadge>
                <span>{config.title}</span>
              </dt>
              <dd
                className='dashboard-stat__value'
                title={!loading && !error ? formatted.fullValue : undefined}
              >
                {loading ? (
                  <Skeleton className='h-8 w-24' />
                ) : error ? (
                  '—'
                ) : (
                  formatted.displayValue
                )}
              </dd>
              <dd className='dashboard-stat__hint'>{config.description}</dd>
            </div>
          )
        })}
      </dl>
    </section>
  )
}
