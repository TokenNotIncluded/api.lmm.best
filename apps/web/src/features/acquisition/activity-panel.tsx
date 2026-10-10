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
import { Activity, AlertTriangle, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { TitledCard } from '@/components/ui/titled-card'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'

export type ActivityState = {
  started_at: number
  scanned_through: number
  updated_at: number
  status: string
  incomplete: boolean
}
export type ActivitySummary = {
  source: string
  eligible_accounts: number
  successful_accounts: number
  incomplete_accounts: number
  mature_accounts: number
  retained_accounts: number
  observing_accounts: number
  retention_rate: number | null
}
export function ActivityPanel({
  state,
  rows,
  canRebuild,
  observedUntil,
  onRefresh,
}: {
  state: ActivityState | null
  rows: ActivitySummary[]
  canRebuild: boolean
  observedUntil: number
  onRefresh: () => Promise<unknown>
}) {
  const { t } = useTranslation()
  const [saving, setSaving] = useState(false)
  const [failed, setFailed] = useState(false)
  const rebuild = async () => {
    setSaving(true)
    setFailed(false)
    try {
      const response = await api.post(
        '/api/admin/acquisition/activity/rebuild',
        {}
      )
      if (!response.data.success) throw new Error('Rebuild failed')
      await onRefresh()
    } catch {
      setFailed(true)
    } finally {
      setSaving(false)
    }
  }
  const summary = rows.reduce<{
    eligible: number
    successful: number
    observing: number
  }>(
    (total, row) => ({
      eligible: total.eligible + row.eligible_accounts,
      successful: total.successful + row.successful_accounts,
      observing: total.observing + row.observing_accounts,
    }),
    { eligible: 0, successful: 0, observing: 0 }
  )
  const peak = rows.reduce(
    (max, row) => Math.max(max, row.successful_accounts),
    0
  )

  return (
    <TitledCard
      title={t('API activation and retention')}
      description={t(
        'Who started calling the API after registering, and who was still calling on day 7.'
      )}
      icon={<Activity aria-hidden='true' />}
      iconTone='chart-1'
      appearance='outlined'
      disableHoverEffect
      contentClassName='space-y-4'
      action={
        canRebuild ? (
          <Button
            type='button'
            variant='outline'
            size='sm'
            disabled={saving || state?.status === 'rebuilding'}
            onClick={() => void rebuild()}
            className='w-full sm:w-auto'
          >
            <RefreshCw
              data-icon='inline-start'
              className={cn('size-3.5', saving && 'animate-spin')}
              aria-hidden='true'
            />
            {t(
              saving || state?.status === 'rebuilding'
                ? 'Rebuilding activity statistics'
                : 'Rebuild activity statistics'
            )}
          </Button>
        ) : undefined
      }
    >
      <details className='bg-muted/40 rounded-lg border px-3 py-2 text-sm'>
        <summary className='cursor-pointer font-medium'>
          {t('Counting rules: text API, UTC day 7')}
        </summary>
        <div className='space-y-3 pt-3'>
          <p className='text-muted-foreground max-w-prose text-sm'>
            {t(
              'Only new accounts within this observation period that explicitly allowed expanded analytics are included. Success requires a delivered text API response with output and no recorded stream or request failure. Creating a key or authorizing OAuth is not success.'
            )}
          </p>
          <p className='text-muted-foreground max-w-prose text-sm'>
            {t(
              'Day 7 retention uses UTC days: accounts with another qualifying request on the seventh day after their first observed success, divided by successful accounts whose full seventh day has been processed. Accounts still under observation are excluded from that denominator.'
            )}
          </p>
        </div>
      </details>
      {!state ? (
        <EmptyState
          icon={Activity}
          title={t('Activity statistics have not started yet.')}
          description={t(
            'Statistics appear after the first rebuild of the operational log.'
          )}
          className='py-10'
          action={
            canRebuild ? (
              <Button
                type='button'
                variant='outline'
                size='sm'
                disabled={saving}
                onClick={() => void rebuild()}
              >
                <RefreshCw
                  data-icon='inline-start'
                  className={cn('size-3.5', saving && 'animate-spin')}
                  aria-hidden='true'
                />
                {t('Rebuild activity statistics')}
              </Button>
            ) : undefined
          }
        />
      ) : (
        <>
          <div className='flex flex-wrap items-center gap-2 text-xs'>
            <Badge variant='outline' className='gap-1.5 tabular-nums'>
              {t('Eligible')}: {summary.eligible}
            </Badge>
            <Badge
              variant='outline'
              className='console-status-success-badge gap-1.5 tabular-nums'
            >
              <span
                className='forge-status-dot-success size-1.5 rounded-full'
                aria-hidden='true'
              />
              {t('Observed successful accounts')}: {summary.successful}
            </Badge>
            <Badge variant='outline' className='gap-1.5 tabular-nums'>
              {t('Under observation')}: {summary.observing}
            </Badge>
          </div>
          <p className='text-muted-foreground text-xs'>
            {t('Data collection started')}:{' '}
            <time dateTime={new Date(state.started_at * 1000).toISOString()}>
              {new Date(state.started_at * 1000).toLocaleString()}
            </time>{' '}
            · {t('Processed through')}:{' '}
            <time
              dateTime={new Date(state.scanned_through * 1000).toISOString()}
            >
              {new Date(state.scanned_through * 1000).toLocaleString()}
            </time>
          </p>
          {(state.status !== 'ready' ||
            state.incomplete ||
            observedUntil - state.updated_at > 180) && (
            <p
              role='status'
              className='console-status-warning-surface flex items-start gap-2 rounded-lg px-3 py-2 text-sm'
            >
              <AlertTriangle
                className='mt-0.5 size-4 shrink-0'
                aria-hidden='true'
              />
              <span>
                {t(
                  state.incomplete
                    ? 'Operational log coverage is incomplete. Affected accounts are excluded from retention rates.'
                    : 'Activity statistics are catching up or temporarily unavailable. Displayed successes are confirmed observations, not a complete current total.'
                )}
              </span>
            </p>
          )}
          {rows.length > 0 && (
            <ul className='space-y-2 sm:hidden'>
              {rows.map((row) => (
                <li key={row.source} className='bg-card rounded-lg border p-3'>
                  <div className='flex items-start justify-between gap-2'>
                    <h3 className='min-w-0 truncate text-sm font-medium'>
                      {row.source === 'unknown'
                        ? t('Direct / unknown source')
                        : row.source}
                    </h3>
                    <Badge
                      variant='outline'
                      className='shrink-0 gap-1.5 tabular-nums'
                    >
                      {row.successful_accounts} / {row.eligible_accounts}
                    </Badge>
                  </div>
                  <div
                    className='bg-muted mt-2 h-1.5 overflow-hidden rounded-full'
                    aria-hidden='true'
                  >
                    <div
                      className='bg-primary/70 h-full rounded-full'
                      style={{
                        width: `${
                          peak
                            ? Math.max(
                                4,
                                Math.round(
                                  (row.successful_accounts / peak) * 100
                                )
                              )
                            : 0
                        }%`,
                      }}
                    />
                  </div>
                  <dl className='mt-2.5 grid grid-cols-2 gap-x-3 gap-y-2 text-xs'>
                    {(
                      [
                        ['Eligible accounts', row.eligible_accounts],
                        [
                          'Observed successful accounts',
                          row.successful_accounts,
                        ],
                        ['Under observation', row.observing_accounts],
                        ['Missing retention data', row.incomplete_accounts],
                        [
                          'Day 7 retained / eligible',
                          `${row.retained_accounts} / ${row.mature_accounts}`,
                        ],
                        [
                          'Day 7 retention',
                          row.retention_rate == null
                            ? t('Not available')
                            : `${(row.retention_rate * 100).toFixed(1)}%`,
                        ],
                      ] as const
                    ).map(([label, value]) => (
                      <div key={label} className='flex flex-col gap-0.5'>
                        <dt className='text-muted-foreground'>{t(label)}</dt>
                        <dd className='font-mono tabular-nums'>{value}</dd>
                      </div>
                    ))}
                  </dl>
                </li>
              ))}
            </ul>
          )}
          {rows.length === 0 ? (
            <EmptyState
              icon={Activity}
              title={t('No eligible accounts in this registration period.')}
              description={t(
                'Widen the reporting period above, or wait until accounts registered in this window begin calling the API.'
              )}
              className='py-10'
            />
          ) : (
            <div
              className='hidden overflow-x-auto rounded-md border sm:block'
              tabIndex={0}
            >
              <table className='w-full text-left text-sm'>
                <thead>
                  <tr className='bg-muted/40'>
                    {[
                      'Source',
                      'Eligible accounts',
                      'Observed successful accounts',
                      'Under observation',
                      'Missing retention data',
                      'Day 7 retained / eligible',
                      'Day 7 retention',
                    ].map((label) => (
                      <th className='border-b p-3 text-xs' key={label}>
                        {t(label)}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {rows.map((row) => (
                    <tr key={row.source} className='hover:bg-muted/30'>
                      <td className='border-b p-3'>
                        {row.source === 'unknown'
                          ? t('Direct / unknown source')
                          : row.source}
                      </td>
                      <td className='border-b p-3 tabular-nums'>
                        {row.eligible_accounts}
                      </td>
                      <td className='border-b p-3 tabular-nums'>
                        {row.successful_accounts}
                      </td>
                      <td className='border-b p-3 tabular-nums'>
                        {row.observing_accounts}
                      </td>
                      <td className='border-b p-3 tabular-nums'>
                        {row.incomplete_accounts}
                      </td>
                      <td className='border-b p-3 tabular-nums'>
                        {row.retained_accounts} / {row.mature_accounts}
                      </td>
                      <td className='border-b p-3 tabular-nums'>
                        {row.retention_rate == null
                          ? t('Not available')
                          : `${(row.retention_rate * 100).toFixed(1)}%`}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
      {failed && (
        <p
          role='alert'
          className='console-status-danger-surface flex items-start gap-2 rounded-lg px-3 py-2 text-sm'
        >
          <AlertTriangle
            className='mt-0.5 size-4 shrink-0'
            aria-hidden='true'
          />
          <span>
            {t('Unable to rebuild activity statistics. Please retry.')}
          </span>
        </p>
      )}
    </TitledCard>
  )
}
