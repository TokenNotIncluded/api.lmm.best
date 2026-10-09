/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery } from '@tanstack/react-query'
import { ChartNoAxesCombined } from 'lucide-react'
import { lazy, Suspense, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { getUserQuotaDates } from '@/features/dashboard/api'
import { useWalletCurrency } from '@/hooks/use-wallet-currency'
import { useAuthStore } from '@/stores/auth-store'

import {
  buildOverviewUsage,
  overviewUsageRange,
  type OverviewMetric,
} from './overview-personalization-data'

const ChartCard = lazy(() =>
  import('@/features/assistant/assistant-visualization').then((module) => ({
    default: module.AssistantVisualizationCard,
  }))
)

export function OverviewUsageCharts() {
  const { t, i18n } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const wallet = useWalletCurrency()
  const [days, setDays] = useState(7)
  const [metric, setMetric] = useState<OverviewMetric>('requests')
  const query = useQuery({
    queryKey: ['overview-recorded-usage', user?.id, days],
    queryFn: async ({ signal }) => {
      const range = overviewUsageRange(days)
      const data = await getUserQuotaDates(range, false, signal)
      if (!data.success || (data.data !== null && !Array.isArray(data.data))) {
        throw new Error('Usage unavailable')
      }
      return { rows: data.data ?? [], ...range }
    },
    enabled: Boolean(user?.id),
    staleTime: 60_000,
    refetchInterval: 60_000,
    retry: false,
  })
  const usage = useMemo(() => {
    if (!query.data) return undefined
    try {
      return buildOverviewUsage(
        query.data.rows,
        metric,
        query.data.start_timestamp,
        query.data.end_timestamp,
        i18n.language,
        t('Other models'),
        t('Unknown model')
      )
    } catch {
      return undefined
    }
  }, [query.data, metric, i18n.language, t])
  const convert = (number: number) =>
    metric === 'quota' ? wallet.quotaToAmount(number) : number
  const name =
    metric === 'quota'
      ? t('Usage cost')
      : metric === 'tokens'
        ? t('Tokens')
        : t('Requests')
  const loading = query.isPending
  const failed = query.isError || (query.isSuccess && !usage)
  const empty = query.isSuccess && !failed && query.data.rows.length === 0
  return (
    <section
      className='overview-usage-charts'
      aria-label={t('Recorded account usage')}
    >
      <header className='overview-chart-toolbar'>
        <div>
          <p className='overview-chart-eyebrow'>{t('Your account only')}</p>
          <h2 className='text-xl font-medium tracking-tight'>
            {t('Usage at a glance')}
          </h2>
        </div>
        <div className='flex flex-wrap items-center gap-2'>
          <div
            className='bg-muted/50 flex rounded-xl p-1'
            role='group'
            aria-label={t('Usage metric')}
          >
            {(['requests', 'tokens', 'quota'] as const).map((value) => (
              <Button
                type='button'
                key={value}
                size='sm'
                variant={metric === value ? 'secondary' : 'ghost'}
                aria-pressed={metric === value}
                onClick={() => setMetric(value)}
              >
                {t(
                  value === 'quota'
                    ? 'Usage cost'
                    : value === 'tokens'
                      ? 'Tokens'
                      : 'Requests'
                )}
              </Button>
            ))}
          </div>
          <select
            className='border-input bg-background h-10 rounded-xl border px-3 text-sm'
            aria-label={t('Time range')}
            value={days}
            onChange={(event) => setDays(Number(event.target.value))}
          >
            <option value={7}>{t('Last 7 days')}</option>
            <option value={30}>{t('Last 30 days')}</option>
          </select>
        </div>
      </header>
      {loading && (
        <div className='overview-chart-grid' aria-busy='true'>
          <Skeleton className='h-80 rounded-2xl' />
          <Skeleton className='h-80 rounded-2xl' />
        </div>
      )}
      {failed && (
        <div className='overview-chart-empty' role='alert'>
          <p>
            {t(
              'Usage could not be loaded. No zero values have been substituted.'
            )}
          </p>
          <Button
            type='button'
            variant='outline'
            onClick={() => void query.refetch()}
          >
            {t('Retry')}
          </Button>
        </div>
      )}
      {empty && (
        <div className='overview-chart-empty'>
          <ChartNoAxesCombined
            className='text-muted-foreground size-8'
            aria-hidden='true'
          />
          <h3 className='font-medium'>
            {t('No recorded usage in this period')}
          </h3>
          <p className='text-muted-foreground text-sm'>
            {t('Your charts will appear after usage is recorded.')}
          </p>
        </div>
      )}
      {!loading && !failed && !empty && usage && (
        <Suspense fallback={<Skeleton className='h-80 rounded-2xl' />}>
          <div className='overview-chart-grid'>
            <ChartCard
              provenance={t('Recorded account usage')}
              visual={{
                kind: 'chart',
                title: t('Daily usage trend'),
                chart_type: 'line',
                labels: usage.labels,
                series: [{ name, values: usage.values.map(convert) }],
                unit: metric === 'quota' ? wallet.label : '',
                source: t('Local dates · {{days}} days', { days }),
              }}
            />
            <ChartCard
              provenance={t('Recorded account usage')}
              visual={{
                kind: 'chart',
                title: t('Model distribution'),
                chart_type: 'donut',
                labels: usage.models.map((row) => row[0]),
                series: [
                  { name, values: usage.models.map((row) => convert(row[1])) },
                ],
                unit: metric === 'quota' ? wallet.label : name,
                source: t('Top five models; the remainder is combined.'),
              }}
            />
          </div>
        </Suspense>
      )}
      <p className='text-muted-foreground mt-3 text-xs'>
        {t(
          'Recorded usage may appear with a delay. Dates follow your browser time zone.'
        )}
      </p>
    </section>
  )
}
