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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { hasPermission } from '@/lib/admin-permissions'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

type Corrections = {
  head: { source: string; revision: number } | null
  has_more: boolean
  items: {
    id: number
    previous_source: string
    source: string
    reason: string
    actor_id: number
    created_at: number
  }[]
}
export function SourceCorrections({ userID }: { userID: number }) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const canRead = hasPermission(user, 'acquisition', 'details')
  const canWrite = canRead && hasPermission(user, 'acquisition', 'write')
  const [source, setSource] = useState('')
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(false)
  const query = useQuery({
    queryKey: ['acquisition-corrections', user?.id, userID],
    enabled: canRead,
    retry: false,
    queryFn: async () => {
      const response = await api.get(
        `/api/admin/acquisition/users/${userID}/corrections`
      )
      if (!response.data.success) throw new Error()
      return response.data.data as Corrections
    },
  })
  if (!canRead) return null
  const save = async () => {
    if (!query.data) return
    setBusy(true)
    setError(false)
    try {
      const response = await api.post(
        `/api/admin/acquisition/users/${userID}/corrections`,
        {
          source: source.trim(),
          reason: reason.trim(),
          expected_revision: query.data.head?.revision ?? 0,
        }
      )
      if (!response.data.success) throw new Error()
      setSource('')
      setReason('')
      await query.refetch()
    } catch {
      setError(true)
      await query.refetch()
    } finally {
      setBusy(false)
    }
  }
  return (
    <section className='space-y-3 border-t pt-4'>
      <h4 className='font-medium'>{t('Manual source correction')}</h4>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Manual corrections are shown separately. Observed attribution, channel totals, invitations and payments remain unchanged.'
        )}
      </p>
      {query.isPending ? (
        <p>{t('Loading')}</p>
      ) : query.isError ? (
        <Button
          type='button'
          variant='outline'
          onClick={() => void query.refetch()}
        >
          {t('Reload source records')}
        </Button>
      ) : (
        query.data && (
          <>
            <p className='text-sm'>
              {query.data.head
                ? `${t('Corrected source')}: ${query.data.head.source}`
                : t('No manual source correction recorded.')}
            </p>
            {canWrite && (
              <div className='space-y-2'>
                <Label htmlFor={`corrected-source-${userID}`}>
                  {t('Corrected source')}
                </Label>
                <Input
                  id={`corrected-source-${userID}`}
                  maxLength={80}
                  value={source}
                  onChange={(event) => setSource(event.target.value)}
                />
                <Label htmlFor={`correction-reason-${userID}`}>
                  {t('Correction reason')}
                </Label>
                <Textarea
                  id={`correction-reason-${userID}`}
                  maxLength={300}
                  value={reason}
                  onChange={(event) => setReason(event.target.value)}
                />
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Explain the evidence without URLs, credentials or personal contact details.'
                  )}
                </p>
                <Button
                  type='button'
                  disabled={busy || !source.trim() || reason.trim().length < 3}
                  onClick={() => void save()}
                >
                  {t('Save correction with audit record')}
                </Button>
                {error && (
                  <p role='alert' className='text-destructive text-sm'>
                    {t(
                      'Correction was not saved. Reloaded the latest version; check the source, reason and consent before retrying.'
                    )}
                  </p>
                )}
              </div>
            )}
            <ol className='space-y-3 text-sm'>
              {query.data.items.map((item) => (
                <li key={item.id} className='border-b pb-2'>
                  <p>
                    {item.previous_source} → {item.source}
                  </p>
                  <p>{item.reason}</p>
                  <p className='text-muted-foreground text-xs'>
                    {t('Operator')} #{item.actor_id} ·{' '}
                    {new Date(item.created_at * 1000).toLocaleString()}
                  </p>
                </li>
              ))}
            </ol>
            {query.data.has_more && (
              <p className='text-muted-foreground text-xs'>
                {t('Showing the latest 100 retained corrections.')}
              </p>
            )}
          </>
        )
      )}
    </section>
  )
}
