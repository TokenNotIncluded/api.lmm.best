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
import { getRouteApi } from '@tanstack/react-router'
/*
Copyright (C) 2026 LIghtJUNction
*/
import {
  AlertCircle,
  ArrowDownWideNarrow,
  CircleCheck,
  CircleHelp,
  CircleX,
  RefreshCw,
  TriangleAlert,
} from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { PublicLayout } from '@/components/layout'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import {
  formatLatency,
  formatThroughput,
  formatUptimePct,
  getSuccessRateDotClass,
  getSuccessRateLevel,
  getSuccessRateTextClass,
  type SuccessRateLevel,
} from '@/features/performance-metrics/lib/format'
import { usePricingData } from '@/features/pricing/hooks/use-pricing-data'
import { cn } from '@/lib/utils'

import { useStatusDetection } from './hooks/use-status-detection'
import { sortStatusGroups } from './lib/aggregate'
import type { StatusGroup, StatusSort } from './types'

const route = getRouteApi('/status/')
const STATUS_HOUR_OPTIONS = [24, 72, 168, 720] as const

function formatStatusTimestamp(
  timestamp: number | null,
  hours: number,
  locale: string,
  t: (key: string, options?: Record<string, unknown>) => string
) {
  if (timestamp === null) {
    return t('Performance window: last {{hours}} hours', { hours })
  }
  const milliseconds =
    timestamp < 1_000_000_000_000 ? timestamp * 1_000 : timestamp
  const date = new Date(milliseconds)
  if (Number.isNaN(date.getTime())) {
    return t('Performance window: last {{hours}} hours', { hours })
  }
  return t('Latest data: {{time}}', {
    time: date.toLocaleString(locale || undefined),
  })
}

function statusLabel(t: (key: string) => string, level: SuccessRateLevel) {
  switch (level) {
    case 'excellent':
      return t('Operational')
    case 'good':
      return t('Operational')
    case 'warning':
      return t('Degraded')
    case 'critical':
      return t('Outage')
    default:
      return t('No data')
  }
}

function statusIcon(level: SuccessRateLevel) {
  switch (level) {
    case 'excellent':
    case 'good':
      return CircleCheck
    case 'warning':
      return TriangleAlert
    case 'critical':
      return CircleX
    default:
      return CircleHelp
  }
}

function SummaryMetric(props: {
  label: string
  value: string
  detail?: string
  className?: string
}) {
  return (
    <div className={cn('min-w-0 px-4 py-3', props.className)}>
      <dt className='text-muted-foreground text-sm sm:text-xs'>
        {props.label}
      </dt>
      <dd className='mt-1 flex min-w-0 items-baseline gap-2'>
        <span className='text-foreground shrink-0 font-mono text-base font-semibold tabular-nums'>
          {props.value}
        </span>
        {props.detail && (
          <span className='text-muted-foreground min-w-0 truncate text-sm sm:text-xs'>
            {props.detail}
          </span>
        )}
      </dd>
    </div>
  )
}

function TtftTrend(props: { values: number[]; label: string }) {
  const values = props.values.filter(
    (value) => Number.isFinite(value) && value > 0
  )
  if (values.length === 0) {
    return (
      <div className='border-border/50 text-muted-foreground flex h-16 items-center justify-center rounded-md border border-dashed text-xs'>
        {props.label}
      </div>
    )
  }

  const max = Math.max(...values)
  return (
    <div className='flex flex-col gap-2'>
      <div className='text-muted-foreground flex items-center justify-between gap-3 text-sm sm:text-xs'>
        <span>{props.label}</span>
        <span className='font-mono tabular-nums'>
          {formatLatency(values.at(-1) ?? Number.NaN)}
        </span>
      </div>
      <div
        className='bg-muted/45 flex h-12 items-end gap-1 rounded-md px-2 py-2 sm:h-14'
        role='img'
        aria-label={`${props.label}: ${values.map(formatLatency).join(', ')}`}
      >
        {values.map((value, index) => (
          <span
            key={`${index}-${value}`}
            title={formatLatency(value)}
            className='bg-primary/45 hover:bg-primary/70 min-w-0 flex-1 rounded-[2px] transition-colors'
            style={{ height: `${Math.max(12, (value / max) * 100)}%` }}
            aria-hidden='true'
          />
        ))}
      </div>
    </div>
  )
}

function SuccessPips(props: { values: number[]; label: string }) {
  const values = props.values.filter(Number.isFinite).slice(-12)
  if (values.length === 0) return null

  return (
    <div
      className='flex items-center gap-1'
      role='img'
      aria-label={`${props.label}: ${values.map(formatUptimePct).join(', ')}`}
    >
      {values.map((value, index) => (
        <span
          key={`${index}-${value}`}
          title={formatUptimePct(value)}
          className={cn('size-1.5 rounded-full', getSuccessRateDotClass(value))}
          aria-hidden='true'
        />
      ))}
    </div>
  )
}

function statusSurfaceClass(level: SuccessRateLevel) {
  switch (level) {
    case 'warning':
      return 'border-warning/35 bg-warning/10'
    case 'critical':
      return 'border-destructive/35 bg-destructive/10'
    default:
      return 'border-border/70 bg-muted/40'
  }
}

function GroupStatusCard(props: {
  group: StatusGroup
  ttftTrendLabel: string
  successTrendLabel: string
}) {
  const { t } = useTranslation()
  const level = getSuccessRateLevel(props.group.successRate)
  const Icon = statusIcon(level)

  return (
    <Card
      data-card-hover='false'
      className='border-border/70 bg-card min-w-0 rounded-xl shadow-none'
      size='sm'
    >
      <CardHeader className='border-border/60 gap-3 border-b px-4 py-3'>
        <div className='min-w-0'>
          <CardTitle
            className='line-clamp-2 min-w-0 text-base leading-5 font-semibold tracking-normal break-words'
            title={props.group.group}
          >
            {props.group.group}
          </CardTitle>
          <span className='text-muted-foreground mt-1 block text-sm sm:text-xs'>
            {t('{{count}} models reporting', { count: props.group.modelCount })}
          </span>
        </div>
        <div className='flex min-w-0 items-center justify-between gap-3'>
          <div
            className={cn(
              'flex min-w-0 items-center gap-1.5 rounded-full border px-2 py-1 text-sm sm:text-xs',
              statusSurfaceClass(level)
            )}
          >
            <Icon
              className={cn(
                'size-3.5 shrink-0',
                getSuccessRateTextClass(props.group.successRate)
              )}
              aria-hidden='true'
            />
            <span className='text-foreground truncate font-medium'>
              {statusLabel(t, level)}
            </span>
            <span className='text-muted-foreground shrink-0 font-mono tabular-nums'>
              {formatUptimePct(props.group.successRate)}
            </span>
          </div>
          <SuccessPips
            values={props.group.successTrend}
            label={props.successTrendLabel}
          />
        </div>
      </CardHeader>
      <CardContent className='flex flex-col gap-3 px-4 py-3'>
        <dl>
          <div className='flex items-baseline justify-between gap-3'>
            <dt className='text-muted-foreground text-sm font-medium sm:text-xs'>
              {t('Average TTFT')}
            </dt>
            <dd className='text-foreground font-mono text-2xl font-semibold tabular-nums'>
              {formatLatency(props.group.avgTtftMs)}
            </dd>
          </div>
        </dl>
        <TtftTrend
          values={props.group.ttftTrend}
          label={props.ttftTrendLabel}
        />
        <dl className='border-border/60 grid grid-cols-2 border-t pt-3'>
          <MetricValue
            label={t('Average latency')}
            value={formatLatency(props.group.avgLatencyMs)}
          />
          <MetricValue
            label={t('Throughput')}
            value={formatThroughput(props.group.avgTps)}
          />
        </dl>
      </CardContent>
    </Card>
  )
}

function MetricValue(props: { label: string; value: string }) {
  return (
    <div className='min-w-0 first:pr-3 last:border-l last:pl-3'>
      <dt className='text-muted-foreground truncate text-sm sm:text-xs'>
        {props.label}
      </dt>
      <dd className='text-foreground mt-1 truncate font-mono text-sm font-semibold tabular-nums'>
        {props.value}
      </dd>
    </div>
  )
}

function StatusDetectionSkeleton() {
  return (
    <div className='space-y-5'>
      <Skeleton className='h-[116px] rounded-lg sm:h-[74px]' />
      <div className='grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3'>
        {Array.from({ length: 6 }).map((_, index) => (
          <Skeleton key={index} className='h-64 rounded-xl' />
        ))}
      </div>
    </div>
  )
}

function EmptyStatusState(props: { message: string }) {
  return (
    <div className='border-border/70 bg-card rounded-lg border border-dashed px-6 py-14 text-center'>
      <AlertCircle
        className='text-muted-foreground/60 mx-auto size-8'
        aria-hidden='true'
      />
      <p className='text-foreground mt-3 text-sm font-semibold'>
        {props.message}
      </p>
    </div>
  )
}

export function StatusDetection() {
  const { t, i18n } = useTranslation()
  const search = route.useSearch()
  const navigate = route.useNavigate()
  const pricing = usePricingData()
  const modelVendors = useMemo(
    () =>
      new Map(
        pricing.models.flatMap((model) =>
          model.vendor_name ? [[model.model_name, model.vendor_name]] : []
        )
      ),
    [pricing.models]
  )
  const vendorOptions = useMemo(
    () =>
      [
        ...new Set(
          pricing.models.map((model) => model.vendor_name).filter(Boolean)
        ),
      ].sort((left, right) =>
        String(left).localeCompare(String(right))
      ) as string[],
    [pricing.models]
  )
  const status = useStatusDetection({
    hours: search.hours,
    model: search.model,
    group: search.group,
    vendor: search.vendor,
    modelVendors,
  })
  const updateSearch = (
    key: 'hours' | 'model' | 'group' | 'vendor',
    value: string
  ) => {
    void navigate({
      replace: true,
      search: (previous) => ({
        ...previous,
        [key]: key === 'hours' ? Number(value) : value || undefined,
      }),
    })
  }
  const clearFilters = () => {
    void navigate({
      replace: true,
      search: (previous) => ({
        ...previous,
        hours: undefined,
        model: undefined,
        group: undefined,
        vendor: undefined,
      }),
    })
  }
  const [sort, setSort] = useState<StatusSort>('ttft')
  const sortedGroups = useMemo(
    () => sortStatusGroups(status.groups, sort),
    [sort, status.groups]
  )
  const fastestGroup = useMemo(
    () => sortStatusGroups(status.groups, 'ttft')[0],
    [status.groups]
  )
  const healthyCount = status.groups.filter((group) =>
    ['excellent', 'good'].includes(getSuccessRateLevel(group.successRate))
  ).length
  const hasError = Boolean(status.error)
  const hasActiveFilters = Boolean(
    search.hours !== 24 || search.model || search.group || search.vendor
  )
  const freshnessLabel = formatStatusTimestamp(
    status.latestTimestamp,
    status.hours,
    i18n.resolvedLanguage || i18n.language,
    t
  )
  const latestTimestampMs =
    status.latestTimestamp === null
      ? null
      : status.latestTimestamp < 1_000_000_000_000
        ? status.latestTimestamp * 1_000
        : status.latestTimestamp
  const dataMayBeDelayed =
    latestTimestampMs !== null &&
    Date.now() - latestTimestampMs > 6 * 60 * 60 * 1_000

  return (
    <PublicLayout
      showMainContainer={false}
      headerProps={{ className: 'forge-public-header' }}
    >
      <main className='min-h-svh'>
        <div className='mx-auto w-full max-w-[1280px] space-y-6 px-3 pt-4 pb-14 sm:px-6 sm:pt-8 xl:px-8'>
          <div className='flex flex-wrap items-end justify-between gap-3'>
            <div className='min-w-0'>
              <h1 className='text-foreground text-2xl font-semibold tracking-normal'>
                {t('Status detection')}
              </h1>
              <p className='text-muted-foreground mt-1 text-sm'>
                {freshnessLabel}
              </p>
            </div>
            <Button
              variant='outline'
              size='sm'
              className='h-11 sm:h-8'
              onClick={() => void status.refresh()}
              disabled={status.isFetching}
              aria-label={t('Refresh')}
            >
              <RefreshCw
                data-icon='inline-start'
                className={cn(
                  status.isFetching && 'animate-spin motion-reduce:animate-none'
                )}
                aria-hidden='true'
              />
              {t('Refresh')}
            </Button>
          </div>

          <div className='border-border/70 bg-card grid gap-3 rounded-lg border p-3 sm:grid-cols-4'>
            <Select
              value={String(search.hours)}
              onValueChange={(value) => value && updateSearch('hours', value)}
            >
              <SelectTrigger
                className='h-10 w-full'
                aria-label={t('Time range')}
              >
                <SelectValue>
                  {t(
                    search.hours === 24
                      ? 'Last 24 hours'
                      : search.hours === 72
                        ? 'Last 3 days'
                        : search.hours === 168
                          ? 'Last 7 days'
                          : 'Last 30 days'
                  )}
                </SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {STATUS_HOUR_OPTIONS.map((hours) => (
                    <SelectItem key={hours} value={String(hours)}>
                      {t(
                        hours === 24
                          ? 'Last 24 hours'
                          : hours === 72
                            ? 'Last 3 days'
                            : hours === 168
                              ? 'Last 7 days'
                              : 'Last 30 days'
                      )}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
            <Select
              value={search.model || 'all'}
              onValueChange={(value) =>
                value && updateSearch('model', value === 'all' ? '' : value)
              }
            >
              <SelectTrigger className='h-10 w-full' aria-label={t('Model')}>
                <SelectValue>{search.model || t('All models')}</SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectItem value='all'>{t('All models')}</SelectItem>
                {status.availableModels.map((model) => (
                  <SelectItem key={model} value={model}>
                    {model}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select
              value={search.group || 'all'}
              onValueChange={(value) =>
                value && updateSearch('group', value === 'all' ? '' : value)
              }
            >
              <SelectTrigger className='h-10 w-full' aria-label={t('Group')}>
                <SelectValue>{search.group || t('All groups')}</SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectItem value='all'>{t('All groups')}</SelectItem>
                {status.availableGroups.map((group) => (
                  <SelectItem key={group} value={group}>
                    {group}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {vendorOptions.length > 0 ? (
              <Select
                value={search.vendor || 'all'}
                onValueChange={(value) =>
                  value && updateSearch('vendor', value === 'all' ? '' : value)
                }
              >
                <SelectTrigger
                  className='h-10 w-full'
                  aria-label={t('Provider')}
                >
                  <SelectValue>
                    {search.vendor || t('All providers')}
                  </SelectValue>
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value='all'>{t('All providers')}</SelectItem>
                  {vendorOptions.map((vendor) => (
                    <SelectItem key={vendor} value={vendor}>
                      {vendor}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : null}
            {hasActiveFilters ? (
              <Button
                variant='ghost'
                className='h-10 justify-self-start sm:col-span-4 sm:justify-self-end'
                onClick={clearFilters}
              >
                {t('Clear filters')}
              </Button>
            ) : null}
          </div>

          <p className='text-muted-foreground text-xs' role='status'>
            {t('Data coverage: {{reported}} of {{total}} models reported.', {
              reported: status.modelsWithData,
              total: status.modelCount,
            })}
          </p>
          {dataMayBeDelayed ? (
            <p className='text-muted-foreground text-xs' role='status'>
              {t(
                'Performance data may be delayed. The latest sample is more than 6 hours old.'
              )}
            </p>
          ) : null}

          {hasError && (
            <Alert variant='destructive'>
              <AlertCircle />
              <AlertTitle>{t('Unable to load status data')}</AlertTitle>
              <AlertDescription className='flex flex-wrap items-center gap-2'>
                <span>{t('Please try again in a moment.')}</span>
                <Button
                  variant='outline'
                  size='xs'
                  onClick={() => void status.refresh()}
                >
                  {t('Retry')}
                </Button>
              </AlertDescription>
            </Alert>
          )}

          {status.isLoading ? (
            <StatusDetectionSkeleton />
          ) : hasError ? null : status.groups.length === 0 ? (
            <EmptyStatusState
              message={
                hasActiveFilters
                  ? t('No models match the current filters.')
                  : t('No status data is available yet.')
              }
            />
          ) : (
            <>
              <dl className='border-border/70 bg-card grid grid-cols-2 overflow-hidden rounded-lg border sm:grid-cols-4'>
                <SummaryMetric
                  className='border-border/60 border-r border-b sm:border-b-0'
                  label={t('Fastest first token')}
                  value={
                    fastestGroup &&
                    Number.isFinite(fastestGroup.avgTtftMs) &&
                    fastestGroup.avgTtftMs > 0
                      ? formatLatency(fastestGroup.avgTtftMs)
                      : '—'
                  }
                  detail={
                    fastestGroup &&
                    Number.isFinite(fastestGroup.avgTtftMs) &&
                    fastestGroup.avgTtftMs > 0
                      ? fastestGroup.group
                      : undefined
                  }
                />
                <SummaryMetric
                  className='border-border/60 border-b sm:border-r sm:border-b-0'
                  label={t('Groups monitored')}
                  value={String(status.groups.length)}
                />
                <SummaryMetric
                  className='border-border/60 border-r'
                  label={t('Operational')}
                  value={String(healthyCount)}
                />
                <SummaryMetric
                  label={t('Models checked')}
                  value={String(status.modelsWithData)}
                />
              </dl>

              {status.failedModelCount > 0 && (
                <div
                  className='bg-muted/40 text-muted-foreground flex items-center gap-2 rounded-md px-3 py-2 text-sm sm:text-xs'
                  role='status'
                >
                  <TriangleAlert
                    className='size-4 shrink-0'
                    aria-hidden='true'
                  />
                  <p>
                    {t('{{count}} model checks could not be completed.', {
                      count: status.failedModelCount,
                    })}
                  </p>
                </div>
              )}

              <section aria-labelledby='status-groups-heading'>
                <div className='mb-3 flex flex-wrap items-center justify-between gap-3'>
                  <div className='flex items-baseline gap-2'>
                    <h2
                      id='status-groups-heading'
                      className='text-foreground text-base font-semibold tracking-normal'
                    >
                      {t('Group status')}
                    </h2>
                    <p className='text-muted-foreground text-sm sm:text-xs'>
                      {t('{{count}} groups', { count: status.groups.length })}
                    </p>
                  </div>
                  <Select
                    value={sort}
                    onValueChange={(value) =>
                      value !== null && setSort(value as StatusSort)
                    }
                  >
                    <SelectTrigger
                      size='sm'
                      className='h-11 w-full sm:h-8 sm:w-52'
                      aria-label={t('Sort groups')}
                    >
                      <SelectValue>
                        <span className='flex min-w-0 items-center gap-2'>
                          <ArrowDownWideNarrow
                            className='text-muted-foreground size-4 shrink-0'
                            aria-hidden='true'
                          />
                          <span className='truncate'>
                            {sort === 'ttft'
                              ? t('Fastest first token')
                              : sort === 'reliability'
                                ? t('Highest success rate')
                                : t('Group name')}
                          </span>
                        </span>
                      </SelectValue>
                    </SelectTrigger>
                    <SelectContent alignItemWithTrigger={false}>
                      <SelectGroup>
                        <SelectItem value='ttft'>
                          {t('Fastest first token')}
                        </SelectItem>
                        <SelectItem value='reliability'>
                          {t('Highest success rate')}
                        </SelectItem>
                        <SelectItem value='name'>{t('Group name')}</SelectItem>
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                </div>
                <div className='grid grid-cols-1 gap-3 sm:gap-4 md:grid-cols-2 xl:grid-cols-3'>
                  {sortedGroups.map((group) => (
                    <GroupStatusCard
                      key={group.group}
                      group={group}
                      ttftTrendLabel={t('First-token trend')}
                      successTrendLabel={t('Success rate trend')}
                    />
                  ))}
                </div>
              </section>
            </>
          )}
        </div>
      </main>
    </PublicLayout>
  )
}
