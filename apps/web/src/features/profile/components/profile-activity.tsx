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
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { toIntlLocale } from '@/i18n/languages'
import { cn } from '@/lib/utils'

import {
  buildProfileActivityCells,
  type ProfileActivityCell,
  type ProfileActivityView,
  type ProfileDailyUsage,
  PROFILE_ACTIVITY_DAYS,
  PROFILE_ACTIVITY_WEEKS,
} from '../lib/activity'

const ACTIVITY_LEVEL_CLASSES = [
  'bg-foreground/[0.08] dark:bg-muted/70',
  'bg-primary/20',
  'bg-primary/35',
  'bg-primary/50',
  'bg-primary/70',
  'bg-primary',
] as const

interface ProfileActivityProps {
  days: ProfileDailyUsage[]
  activeDays: number
  loading: boolean
  error: unknown
  onRetry: () => void
}

interface MonthLabel {
  column: number
  label: string
}

function buildMonthLabels(
  days: ProfileDailyUsage[],
  locale: string
): MonthLabel[] {
  const formatter = new Intl.DateTimeFormat(locale, { month: 'short' })
  const labels: MonthLabel[] = []
  let previousMonth = ''

  for (let column = 0; column < PROFILE_ACTIVITY_WEEKS; column += 1) {
    const date = days[column * 7 + 3]?.date
    if (!date) continue

    const monthKey = `${date.getFullYear()}-${date.getMonth()}`
    if (monthKey === previousMonth) continue

    labels.push({ column: column + 1, label: formatter.format(date) })
    previousMonth = monthKey
  }

  if (labels.length > 1 && labels[1].column - labels[0].column < 4) {
    return labels.slice(1)
  }
  return labels
}

function getCellTitle(
  cell: ProfileActivityCell,
  view: ProfileActivityView,
  dateFormatter: Intl.DateTimeFormat,
  numberFormatter: Intl.NumberFormat,
  tokensLabel: string,
  requestsLabel: string,
  cumulativeLabel: string
): string {
  const tokens = `${numberFormatter.format(cell.displayTokens)} ${tokensLabel}`
  const requests = `${numberFormatter.format(cell.displayRequests)} ${requestsLabel}`

  if (view === 'weekly') {
    const start = dateFormatter.format(cell.periodStart)
    const end = dateFormatter.format(cell.periodEnd)
    return `${start} – ${end}: ${tokens} · ${requests}`
  }

  const date = dateFormatter.format(cell.date)
  return view === 'cumulative'
    ? `${date} · ${cumulativeLabel}: ${tokens} · ${requests}`
    : `${date}: ${tokens} · ${requests}`
}

function ActivityGrid({
  days,
  view,
  locale,
  tokensLabel,
  requestsLabel,
  cumulativeLabel,
}: {
  days: ProfileDailyUsage[]
  view: ProfileActivityView
  locale: string
  tokensLabel: string
  requestsLabel: string
  cumulativeLabel: string
}) {
  const cells = useMemo(
    () => buildProfileActivityCells(days, view),
    [days, view]
  )
  const monthLabels = useMemo(
    () => buildMonthLabels(days, locale),
    [days, locale]
  )
  const monthLabelsByColumn = useMemo(
    () => new Map(monthLabels.map((month) => [month.column, month.label])),
    [monthLabels]
  )
  const dateFormatter = useMemo(
    () =>
      new Intl.DateTimeFormat(locale, {
        year: 'numeric',
        month: 'short',
        day: 'numeric',
      }),
    [locale]
  )
  const numberFormatter = useMemo(
    () =>
      new Intl.NumberFormat(locale, {
        notation: 'compact',
        maximumFractionDigits: 1,
      }),
    [locale]
  )

  return (
    <div className='min-w-[46rem]'>
      <div className='grid grid-flow-col grid-cols-[repeat(53,minmax(0,1fr))] grid-rows-7 gap-1'>
        {cells.map((cell) => (
          <span
            key={cell.dateKey}
            aria-hidden='true'
            title={getCellTitle(
              cell,
              view,
              dateFormatter,
              numberFormatter,
              tokensLabel,
              requestsLabel,
              cumulativeLabel
            )}
            className={cn(
              'aspect-square min-h-2 rounded-[3px] transition-[background-color,box-shadow] duration-150 hover:ring-1 hover:ring-primary/70 motion-reduce:transition-none',
              ACTIVITY_LEVEL_CLASSES[cell.level] ?? ACTIVITY_LEVEL_CLASSES[0]
            )}
          />
        ))}
      </div>
      <div className='text-muted-foreground mt-3 grid grid-cols-[repeat(53,minmax(0,1fr))] gap-1 text-xs'>
        {Array.from({ length: PROFILE_ACTIVITY_WEEKS }, (_, index) => (
          <span key={index} className='whitespace-nowrap'>
            {monthLabelsByColumn.get(index + 1) ?? ''}
          </span>
        ))}
      </div>
    </div>
  )
}

function ActivityGridSkeleton() {
  return (
    <div className='min-w-[46rem]' aria-hidden='true'>
      <div className='grid grid-flow-col grid-cols-[repeat(53,minmax(0,1fr))] grid-rows-7 gap-1'>
        {Array.from({ length: PROFILE_ACTIVITY_DAYS }, (_, index) => (
          <span
            key={index}
            className='bg-muted/70 aspect-square min-h-2 animate-pulse rounded-[3px] motion-reduce:animate-none'
          />
        ))}
      </div>
      <div className='bg-muted/70 mt-3 h-3 w-full max-w-3xl animate-pulse rounded motion-reduce:animate-none' />
    </div>
  )
}

export function ProfileActivity({
  days,
  activeDays,
  loading,
  error,
  onRetry,
}: ProfileActivityProps) {
  const { t, i18n } = useTranslation()
  const [view, setView] = useState<ProfileActivityView>('daily')
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language) ?? 'en'
  const hasActivity = activeDays > 0

  return (
    <section className='space-y-5' aria-labelledby='profile-token-activity'>
      <div className='flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between'>
        <div>
          <h2
            id='profile-token-activity'
            className='!font-sans text-xl font-medium tracking-tight sm:text-2xl'
          >
            {t('Token activity')}
          </h2>
          {!loading && !error && !hasActivity ? (
            <p className='text-muted-foreground mt-1 text-sm'>
              {t('No token activity in the past year')}
            </p>
          ) : null}
        </div>
        <Tabs
          value={view}
          onValueChange={(value) => setView(value as ProfileActivityView)}
          className='w-fit'
        >
          <TabsList variant='line' aria-label={t('Token activity')}>
            <TabsTrigger value='daily' disabled={loading || Boolean(error)}>
              {t('Daily activity')}
            </TabsTrigger>
            <TabsTrigger value='weekly' disabled={loading || Boolean(error)}>
              {t('Weekly')}
            </TabsTrigger>
            <TabsTrigger
              value='cumulative'
              disabled={loading || Boolean(error)}
            >
              {t('Cumulative')}
            </TabsTrigger>
          </TabsList>
        </Tabs>
      </div>

      {error ? (
        <ErrorState
          className='min-h-48 rounded-2xl border'
          title={t('Unable to load token activity')}
          description={t('Try loading the activity again.')}
          onRetry={onRetry}
        />
      ) : (
        <div
          role='img'
          aria-label={t(
            'Token activity for the past year, with {{count}} active days',
            { count: activeDays }
          )}
          className='[scrollbar-width:thin] overflow-x-auto pb-2'
        >
          {loading ? (
            <ActivityGridSkeleton />
          ) : (
            <ActivityGrid
              days={days}
              view={view}
              locale={locale}
              tokensLabel={t('tokens')}
              requestsLabel={t('requests')}
              cumulativeLabel={t('Cumulative')}
            />
          )}
        </div>
      )}
    </section>
  )
}
