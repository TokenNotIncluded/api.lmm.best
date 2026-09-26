/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { Link } from '@tanstack/react-router'
import { ChartNoAxesCombined, RefreshCw } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { useTheme } from '@/context/theme-provider'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatLogQuota } from '@/lib/format'
import { cn } from '@/lib/utils'

import { useModelUsage } from '../hooks/use-model-usage'
import type { ModelUsageRangeKey } from '../lib/model-usage'
import { ModelUsageDonut, type ModelUsageSegment } from './model-usage-donut'

const RANGE_KEYS: ModelUsageRangeKey[] = ['7d', '30d', '365d']
const RANGE_LABELS: Record<ModelUsageRangeKey, string> = {
  '7d': '7 days',
  '30d': '30 days',
  '365d': '365 days',
}
const SEGMENT_COLORS = ['series', 'good', 'warning', 'unknown', 'text']
const MAX_RING_SEGMENTS = 7

export interface ModelUsageCopySnapshot {
  markdown: string
  rangeKey: ModelUsageRangeKey
}

interface ModelUsageReportProps {
  accountCreatedTime?: number
  rangeKey?: ModelUsageRangeKey
  onRangeKeyChange?: (range: ModelUsageRangeKey) => void
  onCopySnapshotChange?: (snapshot: ModelUsageCopySnapshot | null) => void
}

function escapeMarkdownCell(value: string): string {
  return value.replaceAll('|', '\\|').replace(/[\r\n]+/g, ' ')
}

export function ModelUsageReport({
  accountCreatedTime,
  rangeKey: controlledRange,
  onRangeKeyChange,
  onCopySnapshotChange,
}: ModelUsageReportProps) {
  const { t, i18n } = useTranslation()
  const { resolvedTheme } = useTheme()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language) ?? 'en'
  const [localRange, setLocalRange] = useState<ModelUsageRangeKey>('30d')
  const rangeKey = controlledRange ?? localRange
  const [activeIndex, setActiveIndex] = useState<number | null>(null)
  const [hideNumbers, setHideNumbers] = useState(false)
  const query = useModelUsage(rangeKey, accountCreatedTime)
  const { models, totals, shareMetric } = query.report
  const shareLabel = t(
    shareMetric === 'quota'
      ? 'Spend by model'
      : shareMetric === 'tokens'
        ? 'Tokens by model'
        : 'Requests by model'
  )
  const segments = useMemo(() => {
    const result: ModelUsageSegment[] = models
      .slice(0, MAX_RING_SEGMENTS)
      .map((model, index) => ({
        key: model.modelName,
        label:
          model.modelName === 'unknown' ? t('Unknown model') : model.modelName,
        value: model.share,
        color: `var(--forge-chart-${SEGMENT_COLORS[index % SEGMENT_COLORS.length]}-${resolvedTheme === 'dark' ? 'dark' : 'light'})`,
      }))
    const rest = models.slice(MAX_RING_SEGMENTS)
    if (rest.length) {
      result.push({
        key: '__other__',
        label: t('Other models'),
        value: rest.reduce((sum, model) => sum + model.share, 0),
        color: 'var(--muted-foreground)',
      })
    }
    return result
  }, [models, resolvedTheme, t])
  const formatValue = (value: number) =>
    hideNumbers ? '••••' : formatNumber(value, locale)
  const shareFormatter = useMemo(
    () =>
      new Intl.NumberFormat(locale, {
        style: 'percent',
        maximumFractionDigits: 1,
      }),
    [locale]
  )
  const dateFormat = new Intl.DateTimeFormat(locale, { dateStyle: 'medium' })
  const loading = query.isPending && !query.data
  const failed = !query.data && query.isError
  const rangeLabel = `${dateFormat.format(new Date(query.range.start_timestamp * 1000))} – ${dateFormat.format(new Date(query.range.end_timestamp * 1000))}`
  const copyMarkdown = useMemo(() => {
    if (loading || failed || models.length === 0) return ''
    const displayNumber = (value: number) =>
      hideNumbers ? '••••' : formatNumber(value, locale)
    const displayQuota = (value: number) =>
      hideNumbers ? '••••' : String(formatLogQuota(value))
    const rows = models.map(
      (model) =>
        `| ${escapeMarkdownCell(model.modelName === 'unknown' ? t('Unknown model') : model.modelName)} | ${displayNumber(model.tokens)} | ${displayNumber(model.requests)} | ${displayQuota(model.quota)} | ${shareFormatter.format(model.share)} |`
    )
    return [
      `### ${t('Model by model')}`,
      `_${rangeLabel}_`,
      '',
      `- **${t('Total tokens')}**: ${displayNumber(totals.tokens)}`,
      `- **${t('Total requests')}**: ${displayNumber(totals.requests)}`,
      `- **${t('Total Usage')}**: ${displayQuota(totals.quota)}`,
      `- **${t('Models used')}**: ${formatNumber(totals.modelCount, locale)}`,
      '',
      `| ${t('Model')} | ${t('Tokens')} | ${t('Requests')} | ${t('Total Usage')} | ${t('Share')} |`,
      '| --- | ---: | ---: | ---: | ---: |',
      ...rows,
    ].join('\n')
  }, [
    failed,
    shareFormatter,
    hideNumbers,
    loading,
    locale,
    models,
    rangeLabel,
    t,
    totals.modelCount,
    totals.quota,
    totals.requests,
    totals.tokens,
  ])
  useEffect(() => {
    onCopySnapshotChange?.(
      copyMarkdown ? { markdown: copyMarkdown, rangeKey } : null
    )
  }, [copyMarkdown, onCopySnapshotChange, rangeKey])

  return (
    <section
      className='space-y-4 border-b pb-6'
      aria-labelledby='model-usage-heading'
      data-testid='model-usage-report'
    >
      <div className='flex flex-wrap items-start justify-between gap-3'>
        <div className='min-w-0'>
          <h2
            id='model-usage-heading'
            className='text-base font-semibold tracking-tight'
          >
            {t('Model by model')}
          </h2>
          <p className='text-muted-foreground mt-1 text-xs tabular-nums'>
            {rangeLabel}
          </p>
        </div>
        <div className='flex flex-wrap items-center justify-end gap-2'>
          <div
            className='flex flex-wrap gap-1'
            role='group'
            aria-label={t('Time range')}
          >
            {RANGE_KEYS.map((key) => (
              <Button
                key={key}
                size='sm'
                className='min-h-11 sm:min-h-8'
                variant={rangeKey === key ? 'secondary' : 'ghost'}
                aria-pressed={rangeKey === key}
                onClick={() => {
                  setLocalRange(key)
                  onRangeKeyChange?.(key)
                  setActiveIndex(null)
                }}
              >
                {t(RANGE_LABELS[key])}
              </Button>
            ))}
          </div>
          {copyMarkdown ? (
            <CopyButton
              value={copyMarkdown}
              variant='outline'
              size='sm'
              className='min-h-11 sm:min-h-8'
              aria-label={`${t('Copy')} ${t('Statistics')}`}
            >
              {t('Copy')} · {t('Statistics')}
            </CopyButton>
          ) : null}
        </div>
      </div>
      {loading ? (
        <div className='grid gap-5 sm:grid-cols-[15rem_minmax(0,1fr)]'>
          <Skeleton className='mx-auto size-52 rounded-full' />
          <div className='space-y-3'>
            {Array.from({ length: 4 }, (_, index) => (
              <Skeleton key={index} className='h-11 w-full' />
            ))}
          </div>
        </div>
      ) : failed ? (
        <ErrorState
          title={t('Could not load your model breakdown.')}
          description={t('Check your connection and try again.')}
          onRetry={() => void query.refetch()}
        />
      ) : models.length === 0 ? (
        <EmptyState
          icon={ChartNoAxesCombined}
          title={t('No usage in this range yet')}
          description={t('Send one request and your model mix shows up here.')}
          action={
            <Button variant='outline' render={<Link to='/playground' />}>
              {t('Open the playground')}
            </Button>
          }
        />
      ) : (
        <>
          {query.isError && (
            <ErrorState
              className='min-h-0'
              title={t('Could not refresh usage. Showing the last result.')}
              onRetry={() => void query.refetch()}
            />
          )}
          <div className='grid items-center gap-4 sm:grid-cols-[15rem_minmax(0,1fr)]'>
            <ModelUsageDonut
              segments={segments}
              centerValue={formatValue(totals.tokens)}
              centerLabel={t('Total tokens')}
              activeIndex={activeIndex}
              onActiveIndexChange={setActiveIndex}
              label={shareLabel}
            />
            <dl className='grid grid-cols-2 gap-x-4 gap-y-5 sm:grid-cols-1 xl:grid-cols-2'>
              {[
                [t('Total tokens'), formatValue(totals.tokens)],
                [t('Total requests'), formatValue(totals.requests)],
                [
                  t('Total Usage'),
                  hideNumbers ? '••••' : formatLogQuota(totals.quota),
                ],
                [t('Models used'), formatNumber(totals.modelCount, locale)],
              ].map(([label, value]) => (
                <div key={label} className='min-w-0'>
                  <dt className='text-muted-foreground text-xs'>{label}</dt>
                  <dd className='mt-1 text-base font-semibold break-words tabular-nums'>
                    {value}
                  </dd>
                </div>
              ))}
            </dl>
          </div>
          <div className='space-y-2'>
            <p className='text-muted-foreground text-xs'>{shareLabel}</p>
            <ol className='divide-y border-y'>
              {models.map((model, index) => {
                const segmentIndex =
                  index < MAX_RING_SEGMENTS ? index : MAX_RING_SEGMENTS
                return (
                  <li key={model.modelName}>
                    <button
                      type='button'
                      className={cn(
                        'hover:bg-muted/50 focus-visible:ring-ring flex w-full min-w-0 flex-col gap-2 px-2 py-3 text-left transition-colors focus-visible:ring-2 focus-visible:outline-none sm:gap-1',
                        activeIndex === segmentIndex && 'bg-muted/50'
                      )}
                      onPointerEnter={() => setActiveIndex(segmentIndex)}
                      onPointerLeave={() => setActiveIndex(null)}
                      onFocus={() => setActiveIndex(segmentIndex)}
                      onBlur={() => setActiveIndex(null)}
                      onClick={() =>
                        setActiveIndex(
                          activeIndex === segmentIndex ? null : segmentIndex
                        )
                      }
                      aria-pressed={activeIndex === segmentIndex}
                    >
                      <span className='flex min-w-0 items-start justify-between gap-3'>
                        <span className='min-w-0 text-sm font-medium break-all'>
                          {model.modelName === 'unknown'
                            ? t('Unknown model')
                            : model.modelName}
                        </span>
                        <span className='text-muted-foreground shrink-0 text-xs tabular-nums'>
                          {shareFormatter.format(model.share)}
                        </span>
                      </span>
                      <span className='grid grid-cols-3 gap-2 text-xs tabular-nums'>
                        <span className='min-w-0 break-words'>
                          <span className='text-muted-foreground block'>
                            {t('Tokens')}
                          </span>
                          {formatValue(model.tokens)}
                        </span>
                        <span className='min-w-0 break-words'>
                          <span className='text-muted-foreground block'>
                            {t('Requests')}
                          </span>
                          {formatValue(model.requests)}
                        </span>
                        <span className='min-w-0 text-right break-words'>
                          <span className='text-muted-foreground block'>
                            {t('Total Usage')}
                          </span>
                          {hideNumbers ? '••••' : formatLogQuota(model.quota)}
                        </span>
                      </span>
                    </button>
                  </li>
                )
              })}
            </ol>
          </div>
          <div className='flex flex-wrap items-center justify-between gap-3'>
            <label className='flex min-h-11 items-center gap-3 text-sm'>
              <Switch checked={hideNumbers} onCheckedChange={setHideNumbers} />
              {t('Hide the exact numbers')}
            </label>
            <Button
              variant='ghost'
              size='sm'
              className='min-h-11'
              disabled={query.isFetching}
              onClick={() => void query.refetch()}
            >
              <RefreshCw
                className={cn(
                  'size-4',
                  query.isFetching && 'animate-spin motion-reduce:animate-none'
                )}
                aria-hidden='true'
              />
              {t('Refresh')}
            </Button>
          </div>
        </>
      )}
    </section>
  )
}
