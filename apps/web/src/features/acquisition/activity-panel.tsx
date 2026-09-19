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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'

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
  return (
    <section
      className='space-y-4'
      aria-label={t('API activation and retention')}
    >
      <h2 className='text-lg font-semibold'>
        {t('API activation and retention')}
      </h2>
      <details className='space-y-3 text-sm'>
        <summary className='cursor-pointer font-medium'>
          {t('Counting rules: text API, UTC day 7')}
        </summary>
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
      </details>
      {!state ? (
        <p>{t('Activity statistics have not started yet.')}</p>
      ) : (
        <>
          <p className='text-muted-foreground text-xs'>
            {t('Data collection started')}:{' '}
            {new Date(state.started_at * 1000).toLocaleString()} ·{' '}
            {t('Processed through')}:{' '}
            {new Date(state.scanned_through * 1000).toLocaleString()}
          </p>
          {(state.status !== 'ready' ||
            state.incomplete ||
            observedUntil - state.updated_at > 180) && (
            <p role='status'>
              {t(
                state.incomplete
                  ? 'Operational log coverage is incomplete. Affected accounts are excluded from retention rates.'
                  : 'Activity statistics are catching up or temporarily unavailable. Displayed successes are confirmed observations, not a complete current total.'
              )}
            </p>
          )}
          {rows.length > 0 && (
            <div className='space-y-4 sm:hidden'>
              {rows.map((row) => (
                <article key={row.source} className='space-y-3 border-b pb-4'>
                  <h3 className='font-medium'>
                    {row.source === 'unknown'
                      ? t('Direct / unknown source')
                      : row.source}
                  </h3>
                  <dl className='grid grid-cols-2 gap-3 text-sm'>
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
                      <div key={label}>
                        <dt className='text-muted-foreground text-xs'>
                          {t(label)}
                        </dt>
                        <dd className='mt-1 font-medium tabular-nums'>
                          {value}
                        </dd>
                      </div>
                    ))}
                  </dl>
                </article>
              ))}
            </div>
          )}
          {rows.length === 0 ? (
            <p>{t('No eligible accounts in this registration period.')}</p>
          ) : (
            <div className='hidden overflow-x-auto sm:block' tabIndex={0}>
              <table className='w-full text-left text-sm'>
                <thead>
                  <tr>
                    {[
                      'Source',
                      'Eligible accounts',
                      'Observed successful accounts',
                      'Under observation',
                      'Missing retention data',
                      'Day 7 retained / eligible',
                      'Day 7 retention',
                    ].map((label) => (
                      <th className='border-b p-3' key={label}>
                        {t(label)}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {rows.map((row) => (
                    <tr key={row.source}>
                      <td className='border-b p-3'>
                        {row.source === 'unknown'
                          ? t('Direct / unknown source')
                          : row.source}
                      </td>
                      <td className='border-b p-3'>{row.eligible_accounts}</td>
                      <td className='border-b p-3'>
                        {row.successful_accounts}
                      </td>
                      <td className='border-b p-3'>{row.observing_accounts}</td>
                      <td className='border-b p-3'>
                        {row.incomplete_accounts}
                      </td>
                      <td className='border-b p-3'>
                        {row.retained_accounts} / {row.mature_accounts}
                      </td>
                      <td className='border-b p-3'>
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
        <p role='alert'>
          {t('Unable to rebuild activity statistics. Please retry.')}
        </p>
      )}
      {canRebuild && (
        <Button
          variant='outline'
          disabled={saving || state?.status === 'rebuilding'}
          onClick={() => void rebuild()}
        >
          {t(
            saving || state?.status === 'rebuilding'
              ? 'Rebuilding activity statistics'
              : 'Rebuild activity statistics'
          )}
        </Button>
      )}
    </section>
  )
}
