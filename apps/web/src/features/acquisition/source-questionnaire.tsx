/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { SELF_SOURCE_LABELS } from './self-source-labels'

type Report = { source: string; detail: string; updated_at: number }

export function SourceQuestionnaire() {
  const { t } = useTranslation()
  const userID = useAuthStore((state) => state.auth.user?.id)
  const id = useId()
  const [open, setOpen] = useState(false)
  const [source, setSource] = useState('')
  const [detail, setDetail] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(false)
  const query = useQuery({
    queryKey: ['acquisition-self-report', userID],
    enabled: !!userID && open,
    retry: false,
    queryFn: async () => {
      const result = await api.get('/api/acquisition/self-report')
      if (!result.data.success) throw new Error('Source report unavailable')
      return result.data.data as Report | null
    },
  })
  const save = async (remove = false) => {
    setBusy(true)
    setError(false)
    try {
      const result = remove
        ? await api.delete('/api/acquisition/self-report')
        : await api.put('/api/acquisition/self-report', {
            source: source || query.data?.source,
            detail: source ? detail : query.data?.detail || '',
          })
      if (!result.data.success) throw new Error('Source report not saved')
      await query.refetch()
      setSource('')
      setDetail('')
      setOpen(false)
    } catch {
      setError(true)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className='space-y-3 border-t pt-3 text-sm'>
      <Button
        type='button'
        variant='ghost'
        onClick={() => setOpen(!open)}
        aria-expanded={open}
      >
        {t('How did you first hear about LMM? (optional)')}
      </Button>
      {open && (
        <div className='space-y-3'>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Optional source feedback is kept for 365 days, separate from automatic attribution. Skip or delete it anytime. Do not include personal details or credentials.'
            )}
          </p>
          {query.isError ? (
            <Button type='button' onClick={() => void query.refetch()}>
              {t('Retry')}
            </Button>
          ) : query.isPending ? (
            <p>{t('Loading')}</p>
          ) : (
            <>
              <label htmlFor={id}>{t('Source')}</label>
              <select
                id={id}
                className='bg-background block h-9 w-full rounded-md border px-2'
                value={source || query.data?.source || ''}
                onChange={(event) => {
                  setSource(event.target.value)
                  setDetail(query.data?.detail || '')
                }}
              >
                <option value='' disabled>
                  {t('Select')}
                </option>
                {Object.entries(SELF_SOURCE_LABELS).map(([value, label]) => (
                  <option key={value} value={value}>
                    {t(label)}
                  </option>
                ))}
              </select>
              <label htmlFor={`${id}-detail`}>
                {t('Content title or platform (optional, no URLs)')}
              </label>
              <Input
                id={`${id}-detail`}
                value={source ? detail : query.data?.detail || ''}
                maxLength={160}
                onChange={(event) => {
                  setSource(source || query.data?.source || 'other')
                  setDetail(event.target.value)
                }}
              />
              <div className='flex flex-wrap gap-2'>
                <Button
                  type='button'
                  disabled={busy || !(source || query.data?.source)}
                  onClick={() => void save()}
                >
                  {t('Save')}
                </Button>
                <Button
                  type='button'
                  variant='ghost'
                  disabled={busy}
                  onClick={() => setOpen(false)}
                >
                  {t('Skip')}
                </Button>
                {query.data && (
                  <Button
                    type='button'
                    variant='outline'
                    disabled={busy}
                    onClick={() => void save(true)}
                  >
                    {t('Delete')}
                  </Button>
                )}
              </div>
            </>
          )}
          {error && (
            <p role='alert'>
              {t('Unable to save source feedback. Please retry.')}
            </p>
          )}
        </div>
      )}
    </div>
  )
}
