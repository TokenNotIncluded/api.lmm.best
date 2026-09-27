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
import { CalendarRange, Download, Percent, Users } from 'lucide-react'
import { type FormEvent, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { TitledCard } from '@/components/ui/titled-card'
import { hasPermission } from '@/lib/admin-permissions'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import {
  ActivityPanel,
  type ActivityState,
  type ActivitySummary,
} from './activity-panel'
import { AcquisitionFunnelPanel } from './funnel-panel'
import { PromotionLinks } from './promotion-links'
import { customAcquisitionRange, presetAcquisitionRange } from './report-range'
import { sourceSummaryCSV } from './summary-export'
import { SourceUsers } from './user-sources'
import { AcquisitionVisitorPanel } from './visitor-panel'

type Report = {
  from: number
  to: number
  activity_state: ActivityState | null
  activity: ActivitySummary[]
  unclassified_payment_rows: number
  lookback_days: number
  applied_lookback_days: number[]
  channels: {
    source: string
    evidence: string
    registrations: number
    identified_registrations: number
  }[]
  payments: {
    source: string
    currency: string
    paid_micros: number
    refund_micros: number
    net_micros: number
    paying_accounts: number
  }[]
  started_at: number
  observed_until: number
}
async function read<T>(path: string): Promise<T> {
  const res = await api.get(path)
  if (!res.data.success) throw new Error(res.data.message)
  return res.data.data
}

/** Source label shared by both report tables and the channel-account buttons. */
function sourceLabel(source: string, t: (key: string) => string) {
  if (source === 'unknown') return t('Direct / unknown source')
  if (source === 'historical_unrecorded') {
    return t('Historical source not recorded')
  }
  return source
}

function evidenceLabel(evidence: string, t: (key: string) => string) {
  if (evidence === 'promotion_link') return t('Promotion link marker')
  if (evidence === 'campaign_parameters') return t('Campaign parameters')
  if (evidence === 'browser_referrer') {
    return t('Browser-provided source website')
  }
  return t('No identifiable source')
}

type ChannelRow = Report['channels'][number]
type PaymentRow = Report['payments'][number]

/** Registration counts per source. Cards on phones, table from `sm` up. */
function ChannelBreakdown(props: {
  rows: ChannelRow[]
  t: (key: string) => string
}) {
  const { t } = props

  return (
    <>
      <ul className='space-y-2 sm:hidden'>
        {props.rows.map((row) => (
          <li
            key={`${row.source}:${row.evidence}`}
            className='bg-card flex items-start justify-between gap-3 rounded-lg border p-3'
          >
            <div className='min-w-0'>
              <div className='truncate text-sm font-medium'>
                {sourceLabel(row.source, t)}
              </div>
              <div className='text-muted-foreground mt-0.5 text-xs'>
                {evidenceLabel(row.evidence, t)}
              </div>
            </div>
            <span className='shrink-0 font-mono text-sm font-semibold tabular-nums'>
              {row.registrations}
            </span>
          </li>
        ))}
      </ul>

      <div className='hidden overflow-x-auto rounded-md border sm:block'>
        <table className='w-full text-left text-sm'>
          <thead>
            <tr className='bg-muted/40'>
              {['Source', 'Registrations'].map((text) => (
                <th key={text} className='border-b p-3 text-xs'>
                  {t(text)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {props.rows.map((row) => (
              <tr key={`${row.source}:${row.evidence}`}>
                <td className='border-b p-3'>
                  {sourceLabel(row.source, t)}
                  <p className='text-muted-foreground mt-1 text-xs'>
                    {evidenceLabel(row.evidence, t)}
                  </p>
                </td>
                <td className='border-b p-3 tabular-nums'>
                  {row.registrations}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  )
}

/** Settled payment amounts per source and currency. */
function PaymentBreakdown(props: {
  rows: PaymentRow[]
  t: (key: string) => string
}) {
  const { t } = props
  const amounts = (row: PaymentRow) => [
    row.paid_micros,
    row.refund_micros,
    row.net_micros,
  ]

  return (
    <>
      <ul className='space-y-2 sm:hidden'>
        {props.rows.map((row) => (
          <li
            key={`${row.source}:${row.currency}`}
            className='bg-card rounded-lg border p-3'
          >
            <div className='flex items-start justify-between gap-3'>
              <div className='truncate text-sm font-medium'>
                {sourceLabel(row.source, t)}
              </div>
              <span className='text-muted-foreground shrink-0 font-mono text-xs'>
                {row.currency}
              </span>
            </div>
            <dl className='mt-2.5 grid grid-cols-3 gap-x-3 text-xs'>
              {[
                ['Actual payments', row.paid_micros],
                ['Refunds', row.refund_micros],
                ['Net payments', row.net_micros],
              ].map(([label, value]) => (
                <div key={label as string} className='flex flex-col gap-1'>
                  <dt className='text-muted-foreground'>
                    {t(label as string)}
                  </dt>
                  <dd className='font-mono tabular-nums'>
                    {((value as number) / 1e6).toFixed(2)}
                  </dd>
                </div>
              ))}
            </dl>
          </li>
        ))}
      </ul>

      <div className='hidden overflow-x-auto rounded-md border sm:block'>
        <table className='w-full text-left text-sm'>
          <thead>
            <tr className='bg-muted/40'>
              {[
                'Source',
                'Currency',
                'Actual payments',
                'Refunds',
                'Net payments',
              ].map((text) => (
                <th className='border-b p-3 text-xs' key={text}>
                  {t(text)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {props.rows.map((row) => (
              <tr key={`${row.source}:${row.currency}`}>
                <td className='p-3'>{sourceLabel(row.source, t)}</td>
                <td className='p-3 font-mono text-xs'>{row.currency}</td>
                {amounts(row).map((value, i) => (
                  <td className='p-3 tabular-nums' key={i}>
                    {(value / 1e6).toFixed(2)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  )
}

export function Acquisition() {
  const { t } = useTranslation()
  const canWrite = useAuthStore((state) =>
    hasPermission(state.auth.user, 'acquisition', 'write')
  )
  const canReadDetails = useAuthStore((state) =>
    hasPermission(state.auth.user, 'acquisition', 'details')
  )
  const [selectedSource, setSelectedSource] = useState<string | null>(() =>
    new URLSearchParams(window.location.search).get('source')
  )
  const [days, setDays] = useState<number | null>(30)
  const [range, setRange] = useState(() =>
    presetAcquisitionRange(30, Math.floor(Date.now() / 1000))
  )
  const [rangeError, setRangeError] = useState(false)
  const rangeId = useId()
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const report = useQuery({
    queryKey: ['acquisition-report', range.from, range.to],
    refetchInterval: 60_000,
    queryFn: () => {
      return read<Report>(
        `/api/admin/acquisition/report?from=${range.from}&to=${range.to}`
      )
    },
    retry: false,
  })
  const exportSummary = () => {
    if (!report.data) return
    const url = URL.createObjectURL(
      new Blob([sourceSummaryCSV(report.data)], {
        type: 'text/csv;charset=utf-8',
      })
    )
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = 'source-summary.csv'
    document.body.append(anchor)
    anchor.click()
    anchor.remove()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
  const saveLookback = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setError('')
    setSaving(true)
    try {
      const days = Number(new FormData(event.currentTarget).get('days'))
      const response = await api.put('/api/admin/acquisition/lookback', {
        days,
      })
      if (!response.data.success) throw new Error()
      await report.refetch()
    } catch {
      setError(t('Failed to update setting'))
    } finally {
      setSaving(false)
    }
  }
  const registrations =
    report.data?.channels.reduce((sum, row) => sum + row.registrations, 0) ?? 0
  const identified =
    report.data?.channels.reduce(
      (sum, row) => sum + row.identified_registrations,
      0
    ) ?? 0
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('User acquisition')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          variant='outline'
          size='sm'
          disabled={!report.data || report.isError}
          onClick={exportSummary}
        >
          <Download data-icon='inline-start' className='size-3.5' />
          {t('Export summary')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='mx-auto max-w-6xl space-y-6 pb-8'>
          <PromotionLinks canWrite={canWrite} />

          {/* The period picker is the one action this page is really about. */}
          <TitledCard
            title={t('Reporting period')}
            description={t(
              'Choose the window every table and chart below uses.'
            )}
            icon={<CalendarRange aria-hidden='true' />}
            iconTone='info'
            appearance='outlined'
            disableHoverEffect
          >
            <div className='space-y-4'>
              <div className='flex flex-wrap gap-2'>
                {([0, 7, 30] as const).map((value) => (
                  <Button
                    key={value}
                    variant={days === value ? 'default' : 'outline'}
                    onClick={() => {
                      setDays(value)
                      setRange(
                        presetAcquisitionRange(
                          value,
                          Math.floor(Date.now() / 1000)
                        )
                      )
                      setRangeError(false)
                    }}
                  >
                    {value === 0
                      ? t('Today (UTC)')
                      : t('Past {{days}} days', { days: value })}
                  </Button>
                ))}
              </div>

              <form
                className='space-y-2 border-t pt-4'
                onSubmit={(event) => {
                  event.preventDefault()
                  const data = new FormData(event.currentTarget)
                  const value = customAcquisitionRange(
                    String(data.get('start')),
                    String(data.get('end')),
                    Math.floor(Date.now() / 1000)
                  )
                  setRangeError(!value)
                  if (value) {
                    setRange(value)
                    setDays(null)
                  }
                }}
              >
                <div className='flex flex-wrap items-end gap-3'>
                  <label
                    htmlFor={`${rangeId}-start`}
                    className='grid gap-1 text-sm'
                  >
                    {t('Start date')}
                    <Input
                      id={`${rangeId}-start`}
                      type='date'
                      name='start'
                      required
                    />
                  </label>
                  <label
                    htmlFor={`${rangeId}-end`}
                    className='grid gap-1 text-sm'
                  >
                    {t('End date')}
                    <Input
                      id={`${rangeId}-end`}
                      type='date'
                      name='end'
                      required
                    />
                  </label>
                  <Button
                    type='submit'
                    variant={days === null ? 'default' : 'outline'}
                  >
                    {t('Apply date range')}
                  </Button>
                </div>
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Dates use UTC; the end date is included. Up to 366 days.'
                  )}
                </p>
                {rangeError && (
                  <p role='alert' className='text-destructive text-sm'>
                    {t('Choose a valid date range ending no later than today.')}
                  </p>
                )}
              </form>

              <p className='text-muted-foreground border-t pt-4 font-mono text-xs tabular-nums'>
                {t('Selected registration period')}:{' '}
                {new Date(range.from * 1000).toISOString()} →{' '}
                {new Date(range.to * 1000).toISOString()}
              </p>
            </div>
          </TitledCard>

          <TitledCard
            title={t('Registration and payments')}
            description={t(
              'Attribution shows where accounts came from; it does not prove causation.'
            )}
            icon={<Users aria-hidden='true' />}
            iconTone='chart-1'
            appearance='outlined'
            disableHoverEffect
            action={
              <details className='text-sm'>
                <summary className='text-muted-foreground cursor-pointer text-xs font-medium select-none'>
                  {t('How to read these tables')}
                </summary>
                <p className='text-muted-foreground mt-2 max-w-prose text-xs'>
                  {t(
                    'Registration-cohort attribution. Payments are observed through the report update time, grouped by currency. Attribution does not prove causation. API activity has its own processing watermark below.'
                  )}
                </p>
              </details>
            }
          >
            {report.isError ? (
              <ErrorState
                title={t('We could not load the acquisition report.')}
                description={
                  report.error instanceof Error
                    ? report.error.message
                    : undefined
                }
                onRetry={() => void report.refetch()}
                className='min-h-[220px]'
              />
            ) : report.isPending ? (
              <LoadingState message={t('Loading report...')} />
            ) : (
              <div className='space-y-5'>
                <p className='text-muted-foreground text-xs'>
                  {t('Data collection started')}:{' '}
                  {new Date(report.data.started_at * 1000).toLocaleString()} ·{' '}
                  {t('Updated')}:{' '}
                  {new Date(report.data.observed_until * 1000).toLocaleString()}
                </p>

                <div className='grid gap-3 sm:grid-cols-2'>
                  <div className='bg-muted/40 flex items-center justify-between gap-3 rounded-lg px-3 py-2.5'>
                    <span className='text-muted-foreground inline-flex items-center gap-2 text-xs'>
                      <Percent className='size-3.5' aria-hidden='true' />
                      {t('Source coverage')}
                    </span>
                    <span className='font-mono text-sm font-semibold tabular-nums'>
                      {registrations > 0
                        ? `${identified} / ${registrations}`
                        : '-'}
                    </span>
                  </div>
                  <div className='bg-muted/40 flex items-center justify-between gap-3 rounded-lg px-3 py-2.5'>
                    <span className='text-muted-foreground inline-flex items-center gap-2 text-xs'>
                      <Users className='size-3.5' aria-hidden='true' />
                      {t('Registered accounts')}
                    </span>
                    <span className='font-mono text-sm font-semibold tabular-nums'>
                      {registrations}
                    </span>
                  </div>
                </div>

                <div className='space-y-2'>
                  <h3 className='text-sm font-medium'>{t('By source')}</h3>
                  <ChannelBreakdown rows={report.data.channels} t={t} />
                  {registrations === 0 && (
                    <p className='text-muted-foreground text-sm'>
                      {t('No accounts registered in this period.')}
                    </p>
                  )}
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Unknown means no reliable source was recorded; it does not mean the address was typed manually.'
                    )}
                  </p>
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Promotion markers identify the link used, not necessarily the platform where it was seen.'
                    )}
                  </p>
                  {!!report.data.applied_lookback_days?.length && (
                    <p className='text-muted-foreground text-xs'>
                      {t('Recorded attribution windows: {{days}} days', {
                        days: (report.data.applied_lookback_days ?? []).join(
                          ', '
                        ),
                      })}
                    </p>
                  )}
                </div>

                {report.data.unclassified_payment_rows > 0 && (
                  <p
                    role='status'
                    className='console-status-warning-surface rounded-lg px-3 py-2 text-sm'
                  >
                    {t(
                      '{{count}} payment records have incomplete settlement evidence and are excluded.',
                      { count: report.data.unclassified_payment_rows }
                    )}
                  </p>
                )}

                <div className='space-y-2 border-t pt-4'>
                  <h3 className='text-sm font-medium'>{t('Payments')}</h3>
                  <PaymentBreakdown rows={report.data.payments} t={t} />
                </div>
              </div>
            )}
          </TitledCard>

          {report.data && (
            <ActivityPanel
              state={report.data.activity_state ?? null}
              rows={report.data.activity ?? []}
              canRebuild={canWrite}
              observedUntil={report.data.observed_until}
              onRefresh={() => report.refetch()}
            />
          )}
          <AcquisitionVisitorPanel {...range} />
          <AcquisitionFunnelPanel {...range} />
          {canReadDetails && report.data && (
            <TitledCard
              title={t('Channel accounts')}
              description={t(
                'Open the accounts attributed to a single source.'
              )}
              icon={<Users aria-hidden='true' />}
              iconTone='chart-3'
              appearance='outlined'
              disableHoverEffect
            >
              <div className='space-y-4'>
                <div className='flex flex-wrap gap-2'>
                  {[
                    ...new Set(report.data.channels.map((row) => row.source)),
                  ].map((source) => (
                    <Button
                      key={source}
                      variant={
                        selectedSource === source ? 'default' : 'outline'
                      }
                      onClick={() => setSelectedSource(source)}
                    >
                      {sourceLabel(source, t)}
                    </Button>
                  ))}
                </div>
                {selectedSource !== null ? (
                  <SourceUsers
                    key={selectedSource}
                    source={selectedSource}
                    from={report.data.from}
                    to={report.data.to}
                  />
                ) : (
                  <p className='text-muted-foreground text-sm'>
                    {t('Pick a source above to list its accounts.')}
                  </p>
                )}
              </div>
            </TitledCard>
          )}
          {canWrite && report.data && (
            <TitledCard
              title={t('Attribution window')}
              description={t(
                'Changing the default does not rewrite existing source attributions.'
              )}
              icon={<CalendarRange aria-hidden='true' />}
              iconTone='neutral'
              appearance='outlined'
              disableHoverEffect
            >
              <form
                onSubmit={(event) => void saveLookback(event)}
                className='flex flex-wrap items-end gap-3'
              >
                <label className='space-y-1 text-sm'>
                  <span>{t('Lookback days for new registrations')}</span>
                  <Input
                    key={report.data.lookback_days}
                    name='days'
                    type='number'
                    min={1}
                    max={90}
                    required
                    defaultValue={report.data.lookback_days}
                    className='w-32'
                  />
                </label>
                <Button type='submit' disabled={saving}>
                  {t('Save')}
                </Button>
                {error && (
                  <p role='alert' className='text-destructive w-full text-sm'>
                    {error}
                  </p>
                )}
              </form>
            </TitledCard>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
