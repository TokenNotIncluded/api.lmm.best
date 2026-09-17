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
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import { formatQuota, formatTimestamp } from '@/lib/format'

type Entry = {
  id: number
  reward_id: number
  kind: string
  quota: number
  reason: string
  created_at: number
}
type History = {
  entries: Entry[]
  next_cursor: number
  debt_quota: number
  available_quota: number
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
  const request = useRef(0)
  const load = async (before = 0) => {
    const current = ++request.current
    setLoading(true)
    setError('')
    try {
      const response = await api.get<{
        success: boolean
        message?: string
        data: History
      }>('/api/user/self/aff/rewards', {
        params: before ? { before } : undefined,
      })
      if (!response.data.success) {
        throw new Error(
          response.data.message || t('Failed to load referral history')
        )
      }
      if (current !== request.current) return
      const data = response.data.data
      setHistory((previous) => ({
        ...data,
        entries: before
          ? [...(previous?.entries ?? []), ...data.entries]
          : data.entries,
      }))
    } catch (caught) {
      if (current === request.current) {
        setError(
          caught instanceof Error
            ? caught.message
            : t('Failed to load referral history')
        )
      }
    } finally {
      if (current === request.current) setLoading(false)
    }
  }
  return (
    <Dialog
      open={open}
      onOpenChange={(value) => {
        setOpen(value)
        if (value) {
          setHistory(null)
          void load()
        } else {
          request.current++
          setLoading(false)
        }
      }}
      title={t('Referral reward history')}
      description={t(
        'Only the first real paid top-up earns a reward. Confirmed abuse or a full refund can revoke it. Future rewards repay any reward debt first; purchased balance is not deducted.'
      )}
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
              onClick={() => void load(history?.next_cursor ?? 0)}
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
      {!!history?.debt_quota && (
        <p className='mb-4 text-sm'>
          {t('Reward debt')}: {formatQuota(history.debt_quota)}
        </p>
      )}
      {error && (
        <p role='alert' className='text-destructive text-sm'>
          {error}
        </p>
      )}
      {history && history.entries.length === 0 && (
        <p className='text-muted-foreground py-6 text-sm'>
          {t('No referral reward entries yet')}
        </p>
      )}
      <div className='divide-y'>
        {history?.entries.map((entry) => (
          <div
            key={entry.id}
            className='flex items-start justify-between gap-4 py-3 text-sm'
          >
            <div className='min-w-0'>
              <p>{t(kinds[entry.kind] ?? entry.kind)}</p>
              <p className='text-muted-foreground text-xs'>
                {t(reasons[entry.reason] ?? entry.reason)} ·{' '}
                {formatTimestamp(entry.created_at)} · #{entry.reward_id}
              </p>
            </div>
            <span className='shrink-0 tabular-nums'>
              {entry.quota > 0 ? '+' : ''}
              {formatQuota(entry.quota)}
            </span>
          </div>
        ))}
      </div>
      {loading && (
        <p role='status' className='text-muted-foreground py-4 text-sm'>
          {t('Loading...')}
        </p>
      )}
    </Dialog>
  )
}
