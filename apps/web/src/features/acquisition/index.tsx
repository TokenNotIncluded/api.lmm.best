import { useQuery } from '@tanstack/react-query'
import { QRCodeSVG } from 'qrcode.react'
import { type FormEvent, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { hasPermission } from '@/lib/admin-permissions'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import {
  ActivityPanel,
  type ActivityState,
  type ActivitySummary,
} from './activity-panel'
import { AcquisitionCostComparison } from './cost-comparison'
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
import { AcquisitionLinkPreview } from './link-preview'
import { sourceSummaryCSV } from './summary-export'
import { SourceUsers } from './user-sources'

type LinkRecord = {
  id: string
  name: string
  source: string
  medium: string
  campaign: string
  content: string
  target: string
  archived: boolean
}
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
function promotionURL(link: LinkRecord) {
  const url = new URL(link.target, window.location.origin)
  url.searchParams.set('lmm_source', link.id)
  for (const [key, value] of Object.entries({
    utm_source: link.source,
    utm_medium: link.medium,
    utm_campaign: link.campaign,
    utm_content: link.content,
  })) {
    if (value) url.searchParams.set(key, value)
  }
  return url.href
}
export function Acquisition() {
  const { t } = useTranslation()
  const canWrite = useAuthStore((state) =>
    hasPermission(state.auth.user, 'acquisition', 'write')
  )
  const [page, setPage] = useState(1)
  const canReadDetails = useAuthStore((state) =>
    hasPermission(state.auth.user, 'acquisition', 'details')
  )
  const [selectedSource, setSelectedSource] = useState<string | null>(() =>
    new URLSearchParams(window.location.search).get('source')
  )
  const [days, setDays] = useState(30)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [qr, setQR] = useState('')
  const [form, setForm] = useState({
    name: '',
    source: '',
    medium: '',
    campaign: '',
    content: '',
    target: '/',
  })
  const links = useQuery({
    queryKey: ['acquisition-links', page],
    queryFn: () =>
      read<{ items: LinkRecord[]; total: number }>(
        `/api/admin/acquisition/links?page=${page}`
      ),
    retry: false,
  })
  const report = useQuery({
    queryKey: ['acquisition-report', days],
    refetchInterval: 60_000,
    queryFn: () => {
      const to = Math.floor(Date.now() / 1000)
      return read<Report>(
        `/api/admin/acquisition/report?from=${to - days * 86400}&to=${to}`
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
  const save = async (event: FormEvent) => {
    event.preventDefault()
    setSaving(true)
    setError('')
    try {
      const res = await api.post('/api/admin/acquisition/links', form)
      if (!res.data.success) throw new Error(res.data.message)
      await links.refetch()
      setForm({ ...form, name: '' })
    } catch {
      setError(t('Unable to save promotion link'))
    } finally {
      setSaving(false)
    }
  }
  const updateLink = async (link: LinkRecord) => {
    setError('')
    try {
      const res = await api.post('/api/admin/acquisition/links', {
        ...link,
        archived: link.archived,
      })
      if (!res.data.success) throw new Error()
      await links.refetch()
    } catch {
      setError(t('Unable to save promotion link'))
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
        <div className='mx-auto max-w-6xl space-y-8 pb-8'>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Registration-cohort attribution. Payments are observed through the report update time, grouped by currency. Attribution does not prove causation. API activity has its own processing watermark below.'
            )}
          </p>
          <div className='flex flex-wrap gap-2'>
            {[1, 7, 30].map((value) => (
              <Button
                key={value}
                variant={days === value ? 'default' : 'outline'}
                onClick={() => setDays(value)}
              >
                {t('Past {{days}} days', { days: value })}
              </Button>
            ))}
          </div>
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
          <section className='space-y-4'>
            {canWrite && report.data && (
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
              </form>
            )}
            <h2 className='text-lg font-semibold'>{t('Promotion links')}</h2>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Each link keeps a stable identity. Create a new link for a different campaign; renaming or archiving does not rewrite past attribution.'
              )}
            </p>
            {canWrite && (
              <form
                onSubmit={(event) => void save(event)}
                className='grid gap-3 sm:grid-cols-2'
              >
                {(
                  ['name', 'source', 'medium', 'campaign', 'content'] as const
                ).map((field, index) => (
                  <label key={field} className='space-y-1 text-sm'>
                    <span>
                      {t(
                        [
                          'Display name',
                          'Source platform',
                          'Promotion method',
                          'Campaign',
                          'Content label',
                        ][index] ?? 'Content label'
                      )}
                    </span>
                    <Input
                      required={field === 'name' || field === 'source'}
                      maxLength={80}
                      value={form[field]}
                      onChange={(event) =>
                        setForm({ ...form, [field]: event.target.value })
                      }
                    />
                  </label>
                ))}
                <label className='space-y-1 text-sm'>
                  <span>{t('Target page')}</span>
                  <NativeSelect
                    className='border-input h-9 w-full rounded-md border bg-transparent px-3'
                    value={form.target}
                    onChange={(event) =>
                      setForm({ ...form, target: event.target.value })
                    }
                  >
                    {['/', '/guide', '/pricing', '/challenges', '/sign-up'].map(
                      (path) => (
                        <NativeSelectOption key={path} value={path}>
                          {path}
                        </NativeSelectOption>
                      )
                    )}
                  </NativeSelect>
                </label>
                <Button disabled={saving} type='submit'>
                  {t('Create promotion link')}
                </Button>
              </form>
            )}
            {error && <p role='alert'>{error}</p>}
            <div className='flex items-center gap-3'>
              <Button
                variant='outline'
                disabled={page === 1}
                onClick={() => setPage(page - 1)}
              >
                {t('Previous')}
              </Button>
              <span>
                {t('Page')} {page}
              </span>
              <Button
                variant='outline'
                disabled={!links.data || page * 100 >= links.data.total}
                onClick={() => setPage(page + 1)}
              >
                {t('Next')}
              </Button>
            </div>
            {links.isError ? (
              <Button onClick={() => void links.refetch()}>{t('Retry')}</Button>
            ) : links.isPending ? (
              <p>{t('Loading')}</p>
            ) : links.data.items.length === 0 ? (
              <p>{t('No promotion links yet.')}</p>
            ) : (
              <ul className='divide-y'>
                {links.data.items.map((link) => (
                  <li
                    key={link.id}
                    className='flex flex-wrap items-center justify-between gap-3 py-4'
                  >
                    <div>
                      <p className='font-medium'>
                        {link.name} {link.archived && `(${t('Archived')})`}
                      </p>
                      <p className='text-muted-foreground text-sm'>
                        {link.source} / {link.campaign}
                      </p>
                    </div>
                    <div className='flex flex-wrap gap-2'>
                      <CopyButton value={promotionURL(link)} />
                      <Button
                        variant='outline'
                        onClick={() => setQR(qr === link.id ? '' : link.id)}
                      >
                        {t('QR code')}
                      </Button>
                      <AcquisitionLinkPreview id={link.id} />
                      <AcquisitionCostComparison
                        id={link.id}
                        canWrite={canWrite}
                      />
                      {canWrite && (
                        <Button
                          variant='ghost'
                          onClick={() =>
                            void updateLink({
                              ...link,
                              archived: !link.archived,
                            })
                          }
                        >
                          {t(link.archived ? 'Restore' : 'Archive')}
                        </Button>
                      )}
                    </div>
                    {canWrite && (
                      <details className='w-full text-sm'>
                        <summary className='cursor-pointer'>
                          {t('Rename')}
                        </summary>
                        <form
                          className='mt-2 flex gap-2'
                          onSubmit={(event) => {
                            event.preventDefault()
                            const name = new FormData(event.currentTarget).get(
                              'name'
                            )
                            if (typeof name === 'string') {
                              void updateLink({ ...link, name })
                            }
                          }}
                        >
                          <Input
                            name='name'
                            required
                            maxLength={80}
                            defaultValue={link.name}
                            aria-label={t('Display name')}
                          />
                          <Button type='submit'>{t('Save')}</Button>
                        </form>
                      </details>
                    )}
                    {qr === link.id && (
                      <div className='w-full'>
                        <QRCodeSVG value={promotionURL(link)} size={160} />
                      </div>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </section>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
