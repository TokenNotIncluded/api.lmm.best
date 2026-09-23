/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery } from '@tanstack/react-query'
import { GitBranch } from 'lucide-react'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { TitledCard } from '@/components/ui/titled-card'
import { api } from '@/lib/api'

import type { AcquisitionRange } from './report-range'

type Stage = {
  id: string
  observed: number
  not_observed: number
  unknown: number
  mature_observed: number
  mature_known: number
  observing: number
  conversion_rate: number | null
  timing_accounts: number
  mean_seconds_from_registration: number | null
}
type Funnel = {
  observed_until: number
  observation_days: number
  unavailable: string[]
  payment_snapshot_updated_at: number
  payment_snapshot_status: string
  channels: {
    source: string
    registrations: number
    mature_accounts: number
    observing_accounts: number
    stages: Stage[]
  }[]
  first_payment_sources: {
    source: string
    evidence: string
    rule: string
    lookback_days: number
    inferred: boolean
    accounts: number
  }[]
}
const labels: Record<string, string> = {
  application_submitted: 'Access application submitted',
  access_approved: 'API access approved',
  oauth_authorized: 'OAuth authorized',
  api_key_created: 'Manual API key created',
  credential_ready: 'OAuth authorized or key created',
  client_configured: 'Client configuration confirmed',
  first_successful_request: 'First successful request',
  first_payment: 'First successful payment',
  repeat_payment: 'Repeat payment',
}
const accessStages = [
  'application_submitted',
  'access_approved',
  'oauth_authorized',
  'api_key_created',
  'credential_ready',
  'client_configured',
  'first_successful_request',
]
const paymentStages = ['first_payment', 'repeat_payment']
export function AcquisitionFunnelPanel({ from, to }: AcquisitionRange) {
  const { t } = useTranslation()
  const id = useId()
  const [filters, setFilters] = useState({
    source: '',
    campaign: '',
    content: '',
    connection_method: '',
    observation_days: 30,
  })
  const query = useQuery({
    queryKey: ['acquisition-funnel', from, to, filters],
    retry: false,
    queryFn: async () => {
      const params = new URLSearchParams({
        from: String(from),
        to: String(to),
        ...Object.fromEntries(
          Object.entries(filters).map(([k, v]) => [k, String(v)])
        ),
      })
      const result = await api.get(`/api/admin/acquisition/funnel?${params}`)
      if (!result.data.success) throw new Error('Funnel unavailable')
      return result.data.data as Funnel
    },
  })
  const sourceName = (value: string) =>
    value === 'unknown'
      ? t('Direct / unknown source')
      : value === 'historical_unrecorded'
        ? t('Historical source not recorded')
        : value

  const conversionText = (stage: Stage) =>
    stage.conversion_rate === null
      ? t('Unknown')
      : `${(stage.conversion_rate * 100).toFixed(1)}%`

  const timingText = (stage: Stage) =>
    stage.mean_seconds_from_registration === null
      ? t('Unknown')
      : (stage.mean_seconds_from_registration / 3600).toFixed(1)

  const stageTable = (stages: Stage[], wanted: string[]) => {
    const visible = stages.filter((stage) => wanted.includes(stage.id))

    return (
      <>
        {/* Phones: one card per stage. */}
        <ul className='space-y-2 sm:hidden'>
          {visible.map((stage) => (
            <li
              key={stage.id}
              className='bg-card rounded-lg border p-3 text-xs'
            >
              <h5 className='text-sm font-medium'>
                {t(labels[stage.id] || stage.id)}
              </h5>
              <dl className='mt-2 grid grid-cols-2 gap-x-3 gap-y-2'>
                <div className='flex flex-col gap-0.5'>
                  <dt className='text-muted-foreground'>
                    {t('Observed accounts')}
                  </dt>
                  <dd className='font-mono tabular-nums'>{stage.observed}</dd>
                </div>
                <div className='flex flex-col gap-0.5'>
                  <dt className='text-muted-foreground'>
                    {t('No completion observed')}
                  </dt>
                  <dd className='font-mono tabular-nums'>
                    {stage.not_observed}
                  </dd>
                </div>
                <div className='flex flex-col gap-0.5'>
                  <dt className='text-muted-foreground'>{t('Unknown')}</dt>
                  <dd className='font-mono tabular-nums'>{stage.unknown}</dd>
                </div>
                <div className='flex flex-col gap-0.5'>
                  <dt className='text-muted-foreground'>
                    {t('Mature cohort conversion')}
                  </dt>
                  <dd className='font-mono tabular-nums'>
                    {stage.mature_observed} / {stage.mature_known} ·{' '}
                    {conversionText(stage)}
                  </dd>
                </div>
                <div className='col-span-2 flex flex-col gap-0.5'>
                  <dt className='text-muted-foreground'>
                    {t('Average hours since registration')}
                  </dt>
                  <dd className='font-mono tabular-nums'>
                    {timingText(stage)}{' '}
                    <span className='text-muted-foreground'>
                      ({stage.timing_accounts})
                    </span>
                  </dd>
                </div>
              </dl>
            </li>
          ))}
        </ul>

        <div className='hidden overflow-x-auto rounded-md border sm:block'>
          <table className='w-full text-left text-sm'>
            <thead>
              <tr className='bg-muted/40'>
                {[
                  'Stage',
                  'Observed accounts',
                  'No completion observed',
                  'Unknown',
                  'Mature cohort conversion',
                  'Average hours since registration',
                ].map((key) => (
                  <th key={key} className='border-b p-2 text-xs'>
                    {t(key)}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {visible.map((stage) => (
                <tr key={stage.id}>
                  <th className='border-b p-2 font-normal'>
                    {t(labels[stage.id] || stage.id)}
                  </th>
                  <td className='border-b p-2 tabular-nums'>
                    {stage.observed}
                  </td>
                  <td className='border-b p-2 tabular-nums'>
                    {stage.not_observed}
                  </td>
                  <td className='border-b p-2 tabular-nums'>{stage.unknown}</td>
                  <td className='border-b p-2 tabular-nums'>
                    {stage.mature_observed} / {stage.mature_known} ·{' '}
                    {conversionText(stage)}
                  </td>
                  <td className='border-b p-2 tabular-nums'>
                    {timingText(stage)}{' '}
                    <span className='text-muted-foreground'>
                      ({stage.timing_accounts})
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </>
    )
  }
  return (
    <TitledCard
      title={t('Cohort conversion')}
      description={t(
        'Each account is observed for the same number of days after registration. OAuth and manual keys are alternative paths; payment is independent of first use.'
      )}
      icon={<GitBranch aria-hidden='true' />}
      iconTone='chart-2'
      appearance='outlined'
      disableHoverEffect
      contentClassName='space-y-4'
    >
      <form
        className='space-y-3'
        onSubmit={(event) => {
          event.preventDefault()
          const data = new FormData(event.currentTarget)
          setFilters({
            source: String(data.get('source') || ''),
            campaign: String(data.get('campaign') || ''),
            content: String(data.get('content') || ''),
            connection_method: String(data.get('connection_method') || ''),
            observation_days: Number(data.get('observation_days')),
          })
        }}
      >
        <div className='grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-5'>
          {['source', 'campaign', 'content'].map((field, index) => (
            <label
              key={field}
              className='grid gap-1 text-sm'
              htmlFor={`${id}-${field}`}
            >
              {t(['Source', 'Campaign', 'Content'][index])}
              <Input
                id={`${id}-${field}`}
                name={field}
                maxLength={80}
                placeholder={t('Any')}
              />
            </label>
          ))}
          <label className='grid gap-1 text-sm' htmlFor={`${id}-method`}>
            {t('Connection method')}
            <select
              id={`${id}-method`}
              name='connection_method'
              className='bg-background h-9 rounded-md border px-2'
            >
              <option value=''>{t('All')}</option>
              <option value='oauth'>OAuth</option>
              <option value='api_key'>{t('API Key')}</option>
              <option value='both'>{t('Both')}</option>
              <option value='unknown'>{t('Unknown')}</option>
            </select>
          </label>
          <label className='grid gap-1 text-sm' htmlFor={`${id}-days`}>
            {t('Observation days')}
            <Input
              id={`${id}-days`}
              type='number'
              name='observation_days'
              min={1}
              max={90}
              defaultValue={30}
              required
            />
          </label>
        </div>
        <div className='flex flex-wrap items-center gap-3'>
          <Button type='submit'>{t('Apply filters')}</Button>
          <p className='text-muted-foreground text-xs'>
            {t(
              'These filters apply only to this conversion section. Content filters exclude older registrations without a recorded content label.'
            )}
          </p>
        </div>
      </form>
      {query.isError ? (
        <ErrorState
          title={t('We could not load the conversion report.')}
          description={
            query.error instanceof Error ? query.error.message : undefined
          }
          onRetry={() => void query.refetch()}
          className='min-h-[220px]'
        />
      ) : query.isPending ? (
        <LoadingState message={t('Loading report...')} />
      ) : (
        <>
          {!!query.data.unavailable.length && (
            <p role='status'>
              {t(
                'Some stages lack historical evidence. Unknown accounts are not failed conversions.'
              )}
            </p>
          )}
          {query.data.channels.length === 0 && (
            <p>{t('No accounts registered in this period.')}</p>
          )}
          {query.data.channels.map((channel) => (
            <article
              key={channel.source}
              className='space-y-3 rounded-md border p-3'
            >
              <h3 className='font-medium'>{sourceName(channel.source)}</h3>
              <p className='text-muted-foreground text-sm'>
                {t('Registrations')}: {channel.registrations} ·{' '}
                {t('Mature cohort')}: {channel.mature_accounts} ·{' '}
                {t('Observing')}: {channel.observing_accounts}
              </p>
              <h4 className='text-sm font-medium'>{t('API connection')}</h4>
              {stageTable(channel.stages, accessStages)}
              <h4 className='text-sm font-medium'>{t('Payments')}</h4>
              {stageTable(channel.stages, paymentStages)}
            </article>
          ))}
          <p className='text-muted-foreground text-xs'>
            {t(
              'Rates use only accounts with a complete observation window and known evidence. Timings are from registration, with the timing sample count in parentheses.'
            )}
          </p>
          <h3 className='font-medium'>{t('Source before first payment')}</h3>
          {query.data.payment_snapshot_status !== 'ready' && (
            <p role='status'>
              {t(
                'Payment attribution is still processing or unavailable; missing snapshots are not zero conversions.'
              )}
            </p>
          )}
          <p className='text-muted-foreground text-sm'>
            {t(
              'Payment attribution uses saved evidence and its recorded lookback window. It does not change registration attribution or prove causation.'
            )}
          </p>
          <p className='text-muted-foreground text-xs'>
            {t('Updated')}:{' '}
            {query.data.payment_snapshot_updated_at
              ? new Date(
                  query.data.payment_snapshot_updated_at * 1000
                ).toLocaleString()
              : t('Not available')}
          </p>
          {query.data.first_payment_sources.length === 0 ? (
            <p>{t('No payment attribution recorded yet.')}</p>
          ) : (
            <ul className='divide-y'>
              {query.data.first_payment_sources.map((row, index) => (
                <li key={index} className='space-y-1 py-3 text-sm'>
                  <p>
                    {sourceName(row.source)} · {row.accounts}
                  </p>
                  <p className='text-muted-foreground'>
                    {row.inferred
                      ? t('Attributed from earlier source records')
                      : t('No identifiable source')}{' '}
                    ·{' '}
                    {t('Recorded attribution windows: {{days}} days', {
                      days: row.lookback_days,
                    })}
                  </p>
                  <details>
                    <summary className='cursor-pointer'>
                      {t('Attribution evidence')}
                    </summary>
                    <code>
                      {row.rule} · {row.evidence}
                    </code>
                  </details>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </TitledCard>
  )
}
