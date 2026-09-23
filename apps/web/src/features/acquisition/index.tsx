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
/*
Copyright (C) 2026 LIghtJUNction
*/
import { useQuery } from '@tanstack/react-query'
import { type FormEvent, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { hasPermission } from '@/lib/admin-permissions'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import {
  ActivityPanel,
  type ActivityState,
  type ActivitySummary,
} from './activity-panel'
import { AcquisitionFunnelPanel } from './funnel-panel'
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
  const registrations = report.data?.channels.reduce(
    (sum, row) => sum + row.registrations,
    0
  )
  const identified = report.data?.channels.reduce(
    (sum, row) => sum + row.identified_registrations,
    0
  )
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('User acquisition')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='mx-auto max-w-6xl space-y-6 pb-8'>
          <PromotionLinks canWrite={canWrite} />
          <div className='space-y-1'>
            <h2 className='text-lg font-semibold'>
              {t('Registration and payments')}
            </h2>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Registration-cohort attribution. Payments are observed through the report update time, grouped by currency. Attribution does not prove causation. API activity has its own processing watermark below.'
              )}
            </p>
          </div>
          <div className='flex flex-wrap gap-2'>
            {([0, 7, 30] as const).map((value) => (
              <Button
                key={value}
                variant={days === value ? 'default' : 'outline'}
                onClick={() => {
                  setDays(value)
                  setRange(
                    presetAcquisitionRange(value, Math.floor(Date.now() / 1000))
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
            className='space-y-2'
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
              <label htmlFor={`${rangeId}-end`} className='grid gap-1 text-sm'>
                {t('End date')}
                <Input id={`${rangeId}-end`} type='date' name='end' required />
              </label>
              <Button
                type='submit'
                variant={days === null ? 'default' : 'outline'}
              >
                {t('Apply date range')}
              </Button>
            </div>
            <p className='text-muted-foreground text-xs'>
              {t('Dates use UTC; the end date is included. Up to 366 days.')}
            </p>
            {rangeError && (
              <p role='alert'>
                {t('Choose a valid date range ending no later than today.')}
              </p>
            )}
          </form>
          <p className='text-muted-foreground text-xs'>
            {t('Selected registration period')}:{' '}
            {new Date(range.from * 1000).toISOString()} →{' '}
            {new Date(range.to * 1000).toISOString()}
          </p>
          <Button
            variant='outline'
            disabled={!report.data || report.isError}
            onClick={exportSummary}
          >
            {t('Export summary')}
          </Button>
          {report.isError ? (
            <Button onClick={() => void report.refetch()}>
              {t('Reload report')}
            </Button>
          ) : report.isPending ? (
            <p>{t('Loading')}</p>
          ) : (
            <section className='space-y-3'>
              <p className='text-muted-foreground text-xs'>
                {t('Data collection started')}:{' '}
                {new Date(report.data.started_at * 1000).toLocaleString()} ·{' '}
                {t('Updated')}:{' '}
                {new Date(report.data.observed_until * 1000).toLocaleString()}
              </p>
              <p>
                {t(
                  'Source coverage: {{identified}} / {{total}} registered accounts',
                  { identified, total: registrations }
                )}
              </p>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Promotion markers identify the link used, not necessarily the platform where it was seen.'
                )}
              </p>
              <div className='overflow-x-auto'>
                <table className='w-full text-left text-sm'>
                  <thead>
                    <tr>
                      {['Source', 'Registrations'].map((text) => (
                        <th key={text} className='border-b p-3'>
                          {t(text)}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {report.data.channels.map((row) => (
                      <tr key={`${row.source}:${row.evidence}`}>
                        <td className='border-b p-3'>
                          {row.source === 'unknown'
                            ? t('Direct / unknown source')
                            : row.source === 'historical_unrecorded'
                              ? t('Historical source not recorded')
                              : row.source}
                          <p className='text-muted-foreground mt-1 text-xs'>
                            {t(
                              row.evidence === 'promotion_link'
                                ? 'Promotion link marker'
                                : row.evidence === 'campaign_parameters'
                                  ? 'Campaign parameters'
                                  : row.evidence === 'browser_referrer'
                                    ? 'Browser-provided source website'
                                    : 'No identifiable source'
                            )}
                          </p>
                        </td>
                        <td className='border-b p-3'>{row.registrations}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              {registrations === 0 && (
                <p>{t('No accounts registered in this period.')}</p>
              )}
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Unknown means no reliable source was recorded; it does not mean the address was typed manually.'
                )}
              </p>
              {!!report.data.applied_lookback_days?.length && (
                <p className='text-muted-foreground text-xs'>
                  {t('Recorded attribution windows: {{days}} days', {
                    days: (report.data.applied_lookback_days ?? []).join(', '),
                  })}
                </p>
              )}
              {report.data.unclassified_payment_rows > 0 && (
                <p role='status'>
                  {t(
                    '{{count}} payment records have incomplete settlement evidence and are excluded.',
                    { count: report.data.unclassified_payment_rows }
                  )}
                </p>
              )}
              <div className='overflow-x-auto'>
                <table className='w-full text-left text-sm'>
                  <thead>
                    <tr>
                      {[
                        'Source',
                        'Currency',
                        'Actual payments',
                        'Refunds',
                        'Net payments',
                      ].map((text) => (
                        <th className='border-b p-3' key={text}>
                          {t(text)}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {report.data.payments.map((row) => (
                      <tr key={`${row.source}:${row.currency}`}>
                        <td className='p-3'>
                          {row.source === 'unknown'
                            ? t('Direct / unknown source')
                            : row.source === 'historical_unrecorded'
                              ? t('Historical source not recorded')
                              : row.source}
                        </td>
                        <td className='p-3'>{row.currency}</td>
                        {[
                          row.paid_micros,
                          row.refund_micros,
                          row.net_micros,
                        ].map((value, i) => (
                          <td className='p-3 tabular-nums' key={i}>
                            {(value / 1e6).toFixed(2)}
                          </td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>
          )}
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
            <div className='space-y-4'>
              <div className='flex flex-wrap gap-2'>
                {[
                  ...new Set(report.data.channels.map((row) => row.source)),
                ].map((source) => (
                  <Button
                    key={source}
                    variant='outline'
                    onClick={() => setSelectedSource(source)}
                  >
                    {t('Channel accounts')}:{' '}
                    {source === 'unknown'
                      ? t('Direct / unknown source')
                      : source}
                  </Button>
                ))}
              </div>
              {selectedSource !== null && (
                <SourceUsers
                  key={selectedSource}
                  source={selectedSource}
                  from={report.data.from}
                  to={report.data.to}
                />
              )}
            </div>
          )}
          {canWrite && report.data && (
            <section className='border-t pt-6'>
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
                  />
                </label>
                <Button type='submit' disabled={saving}>
                  {t('Save')}
                </Button>
                <p className='text-muted-foreground w-full text-xs'>
                  {t(
                    'Changing the default does not rewrite existing source attributions.'
                  )}
                </p>
                {error && (
                  <p role='alert' className='text-destructive w-full text-sm'>
                    {error}
                  </p>
                )}
              </form>
            </section>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
