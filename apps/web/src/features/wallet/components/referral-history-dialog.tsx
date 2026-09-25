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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import { formatQuota, formatTimestamp } from '@/lib/format'

import { createReferralHistoryRequests } from '../lib/referral-history-requests'

type Entry = {
  id: number
  reward_id: number
  kind: string
  quota: number
  reason: string
  created_at: number
}
type ReferralPolicy = {
  reward_quota: number
  min_top_up_quota: number
  max_reward_quota: number
  penalty_percent: number
  max_penalty_quota: number
}
type History = {
  entries: Entry[]
  next_cursor: number
  debt_quota: number
  available_quota: number
  policy?: ReferralPolicy
}
const kinds: Record<string, string> = {
  reward: 'First top-up reward',
  clawback: 'Reward clawback',
  penalty: 'Additional penalty',
  restore_reward: 'Reward restored',
  restore_penalty: 'Penalty reversed',
}
const reasons: Record<string, string> = {
  first_top_up: 'First real paid top-up',
  abuse: 'Confirmed abuse',
  bulk_registration: 'Bulk registration',
  refund: 'Full refund',
  mistaken_ban: 'Mistaken ban',
}

export function ReferralHistoryDialog() {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [history, setHistory] = useState<History | null>(null)
  const [failedCursor, setFailedCursor] = useState(0)
  const [requests] = useState(createReferralHistoryRequests)
  useEffect(() => () => requests.cancel(), [requests])

  const load = async (before = 0) => {
    const current = requests.begin(before)
    if (!current) return
    setLoading(true)
    setError('')
    try {
      const response = await api.get<{
        success: boolean
        message?: string
        data: History
      }>('/api/user/self/aff/rewards', {
        params: before ? { before } : undefined,
        signal: current.controller.signal,
        // This dialog owns cancellation; do not reuse another GET promise.
        disableDuplicate: true,
        skipBusinessError: true,
        skipErrorHandler: true,
      })
      if (!requests.isCurrent(current)) return
      if (!response.data.success) {
        throw new Error(
          response.data.message || t('Failed to load referral history')
        )
      }
      const data = response.data.data
      setHistory((previous) => ({
        ...data,
        entries: before
          ? [...(previous?.entries ?? []), ...data.entries]
          : data.entries,
      }))
    } catch (caught) {
      if (requests.isCurrent(current)) {
        setFailedCursor(current.before)
        setError(
          caught instanceof Error
            ? caught.message
            : t('Failed to load referral history')
        )
      }
    } finally {
      if (requests.finish(current)) setLoading(false)
    }
  }
  return (
    <Dialog
      open={open}
      onOpenChange={(value) => {
        requests.cancel()
        setOpen(value)
        setHistory(null)
        setError('')
        setLoading(false)
        if (value) void load()
      }}
      title={t('Referral reward history')}
      description={t(
        'Only the first real paid top-up earns a reward. Confirmed abuse or a full refund can revoke it. Future rewards repay any reward debt first; purchased balance is not deducted.'
      )}
      contentClassName='sm:max-w-3xl'
      bodyClassName='space-y-4'
      footerClassName='border-border/60 border-t bg-muted/20'
      trigger={
        <Button variant='ghost' size='sm'>
          {t('Reward history')}
        </Button>
      }
      footer={
        <>
          {error && (
            <Button
              variant='outline'
              disabled={loading}
              onClick={() => void load(failedCursor)}
            >
              {t('Retry')}
            </Button>
          )}
          {!!history?.next_cursor && !error && (
            <Button
              variant='outline'
              disabled={loading}
              onClick={() => void load(history.next_cursor)}
            >
              {t('Load more')}
            </Button>
          )}
        </>
      }
    >
      {history && (
        <div
          className={
            history.policy
              ? 'grid grid-cols-1 gap-2 sm:grid-cols-3'
              : 'grid grid-cols-1 gap-2 sm:grid-cols-2'
          }
        >
          <div className='border-border/70 bg-muted/25 rounded-2xl border px-4 py-3'>
            <p className='text-muted-foreground text-xs font-medium tracking-wide uppercase'>
              {t('Available Rewards')}
            </p>
            <p className='mt-1 text-xl font-semibold tabular-nums'>
              {formatQuota(history.available_quota)}
            </p>
          </div>
          <div className='border-border/70 bg-muted/25 rounded-2xl border px-4 py-3'>
            <p className='text-muted-foreground text-xs font-medium tracking-wide uppercase'>
              {t('Reward debt')}
            </p>
            <p
              className={
                history.debt_quota > 0
                  ? 'text-destructive mt-1 text-xl font-semibold tabular-nums'
                  : 'mt-1 text-xl font-semibold tabular-nums'
              }
            >
              {formatQuota(history.debt_quota)}
            </p>
          </div>
          {history.policy && (
            <div className='border-border/70 bg-muted/25 rounded-2xl border px-4 py-3'>
              <p className='text-muted-foreground text-xs font-medium tracking-wide uppercase'>
                {t('First top-up reward')}
              </p>
              <p className='mt-1 text-xl font-semibold tabular-nums'>
                {formatQuota(history.policy.reward_quota)}
              </p>
            </div>
          )}
        </div>
      )}

      {error && (
        <div
          role='alert'
          className='border-destructive/30 bg-destructive/5 text-destructive rounded-2xl border px-4 py-3 text-sm'
        >
          {error}
        </div>
      )}

      {history && history.entries.length === 0 && (
        <div className='border-border/70 bg-muted/15 rounded-3xl border border-dashed px-5 py-10 text-center'>
          <p className='text-muted-foreground text-sm'>
            {t('No referral reward entries yet')}
          </p>
        </div>
      )}

      {history && history.entries.length > 0 && (
        <div className='space-y-2'>
          {history.entries.map((entry) => (
            <div
              key={entry.id}
              className='border-border/70 bg-background hover:bg-muted/20 flex items-start justify-between gap-4 rounded-2xl border px-4 py-3.5 text-sm transition-colors'
            >
              <div className='min-w-0'>
                <div className='flex flex-wrap items-center gap-2'>
                  <p className='font-medium'>
                    {t(kinds[entry.kind] ?? entry.kind)}
                  </p>
                  <span className='border-border/70 bg-muted/40 text-muted-foreground rounded-full border px-2 py-0.5 text-[11px] tabular-nums'>
                    #{entry.reward_id}
                  </span>
                </div>
                <p className='text-muted-foreground mt-1 text-xs leading-5'>
                  {t(reasons[entry.reason] ?? entry.reason)} ·{' '}
                  {formatTimestamp(entry.created_at)}
                </p>
              </div>
              <span
                className={
                  entry.quota < 0
                    ? 'text-destructive shrink-0 font-medium tabular-nums'
                    : 'shrink-0 font-medium tabular-nums'
                }
              >
                {entry.quota > 0 ? '+' : ''}
                {formatQuota(entry.quota)}
              </span>
            </div>
          ))}
        </div>
      )}

      {loading && (
        <div
          role='status'
          className='border-border/60 bg-muted/15 rounded-2xl border px-4 py-3'
        >
          <div className='flex items-center gap-3'>
            <span className='bg-foreground/45 size-1.5 animate-pulse rounded-full' />
            <span className='text-muted-foreground text-sm'>
              {t('Loading...')}
            </span>
          </div>
        </div>
      )}
    </Dialog>
  )
}
