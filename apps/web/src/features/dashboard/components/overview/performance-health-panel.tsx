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
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { getPerfMetricsSummary } from '@/features/performance-metrics/api'
import {
  formatLatency,
  formatThroughput,
  formatUptimePct,
  getSuccessRateDotClass,
  getSuccessRateTextClass,
} from '@/features/performance-metrics/lib/format'
import type { PerfModelSummary } from '@/features/performance-metrics/types'
import { cn } from '@/lib/utils'

const PERFORMANCE_WINDOW_HOURS = 24
const TOP_MODEL_LIMIT = 6

type WeightedMetric = 'avg_latency_ms' | 'avg_tps' | 'success_rate'

function simpleAverage(
  rows: PerfModelSummary[],
  metric: WeightedMetric,
  isValid: (value: number) => boolean
): number {
  let total = 0
  let count = 0
  for (const row of rows) {
    const value = Number(row[metric])
    if (!isValid(value)) continue
    total += value
    count++
  }
  return count > 0 ? total / count : Number.NaN
}

export function PerformanceHealthPanel() {
  const { t } = useTranslation()
  const metricsQuery = useQuery({
    queryKey: ['perf-metrics-summary', PERFORMANCE_WINDOW_HOURS],
    queryFn: () => getPerfMetricsSummary(PERFORMANCE_WINDOW_HOURS),
    staleTime: 60 * 1000,
    retry: false,
  })

  const models = useMemo(
    () => metricsQuery.data?.data?.models ?? [],
    [metricsQuery.data]
  )

  const summary = useMemo(() => {
    return {
      avgLatencyMs: Math.round(
        simpleAverage(
          models,
          'avg_latency_ms',
          (v) => Number.isFinite(v) && v > 0
        )
      ),
      avgTps: simpleAverage(
        models,
        'avg_tps',
        (v) => Number.isFinite(v) && v > 0
      ),
      successRate: simpleAverage(models, 'success_rate', Number.isFinite),
    }
  }, [models])

  const hasTraffic =
    models.length > 0 &&
    models.every(
      (model) =>
        Number.isFinite(model.request_count) && Number(model.request_count) >= 0
    )
  const topModels = useMemo(
    () =>
      (hasTraffic
        ? [...models].sort(
            (a, b) => Number(b.request_count) - Number(a.request_count)
          )
        : models
      ).slice(0, TOP_MODEL_LIMIT),
    [models, hasTraffic]
  )
  const failed = metricsQuery.isError || metricsQuery.data?.success === false
  const loading = metricsQuery.isLoading
  const hasData = models.length > 0

  return (
    <section className='border-border min-w-0 border-b pb-8'>
      <div className='mb-6 flex flex-wrap items-baseline justify-between gap-2'>
        <h3 className='text-base font-semibold'>{t('Performance health')}</h3>
        <span className='text-muted-foreground text-xs'>
          {t('Performance metrics for the last 24 hours')}
        </span>
      </div>
      {failed ? (
        <div
          role='status'
          className='text-muted-foreground flex flex-wrap items-center gap-3 text-sm'
        >
          {t('Failed to load data')}
          <Button
            type='button'
            variant='ghost'
            size='sm'
            disabled={metricsQuery.isFetching}
            onClick={() => void metricsQuery.refetch()}
          >
            {t('Retry')}
          </Button>
        </div>
      ) : !loading && !hasData ? (
        <div className='text-muted-foreground flex flex-wrap items-center gap-3 text-sm'>
          <span>{t('No data available')}</span>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            disabled={metricsQuery.isFetching}
            onClick={() => void metricsQuery.refetch()}
          >
            {t('Refresh')}
          </Button>
        </div>
      ) : (
        <div className='space-y-6'>
          <div className='grid grid-cols-1 gap-5 sm:grid-cols-3 sm:gap-8'>
            <MetricCell
              label={t('Success rate')}
              value={formatUptimePct(summary.successRate)}
              loading={loading}
              valueClassName={getSuccessRateTextClass(summary.successRate)}
            />
            <MetricCell
              label={t('Average latency')}
              value={formatLatency(summary.avgLatencyMs)}
              loading={loading}
            />
            <MetricCell
              label={t('Throughput')}
              value={formatThroughput(summary.avgTps)}
              loading={loading}
            />
          </div>

          {loading ? (
            <div className='space-y-1.5'>
              {['success', 'latency', 'throughput'].map((key) => (
                <Skeleton key={key} className='h-6 w-full rounded-lg' />
              ))}
            </div>
          ) : (
            hasData && (
              <div className='border-border border-t pt-5'>
                <div className='mb-2 flex items-center justify-between'>
                  <span className='text-muted-foreground text-[11px] font-medium'>
                    {t(hasTraffic ? 'Top models by traffic' : 'Models')}
                  </span>
                  <span className='text-muted-foreground text-xs tabular-nums'>
                    {topModels.length} {t('Models')}
                  </span>
                </div>
                <div className='grid grid-cols-1 gap-1.5 sm:grid-cols-2'>
                  {topModels.map((model) => (
                    <div
                      key={model.model_name}
                      className='flex min-w-0 items-start justify-between gap-3 py-2'
                    >
                      <span className='min-w-0 flex-1 font-mono text-xs font-medium [overflow-wrap:anywhere]'>
                        {model.model_name}
                      </span>
                      <span className='inline-flex shrink-0 items-center gap-1.5 py-0.5'>
                        <span
                          className={cn(
                            'size-1.5 rounded-full',
                            getSuccessRateDotClass(model.success_rate)
                          )}
                          aria-hidden='true'
                        />
                        <span
                          className={cn(
                            'font-mono text-[11px] font-semibold tabular-nums',
                            getSuccessRateTextClass(model.success_rate)
                          )}
                        >
                          {formatUptimePct(model.success_rate)}
                        </span>
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            )
          )}
        </div>
      )}
    </section>
  )
}

function MetricCell(props: {
  label: string
  value: string
  loading: boolean
  valueClassName?: string
}) {
  return (
    <div className='min-w-0'>
      <div className='text-muted-foreground text-sm'>{props.label}</div>
      {props.loading ? (
        <Skeleton className='mt-2 h-5 w-16' />
      ) : (
        <div
          className={cn(
            'mt-2 text-xl font-semibold tabular-nums',
            props.valueClassName
          )}
        >
          {props.value}
        </div>
      )}
    </div>
  )
}
