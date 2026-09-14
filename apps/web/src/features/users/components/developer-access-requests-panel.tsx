/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { Check, RefreshCw, X } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'

import {
  listDeveloperAccessRequests,
  reviewDeveloperAccessRequest,
  type DeveloperAccessRequestAdmin,
} from '../api'

export function DeveloperAccessRequestsPanel(props: {
  focusRequestId?: number
}) {
  const { t } = useTranslation()
  const [reviewing, setReviewing] = useState<number | null>(null)
  const [notes, setNotes] = useState<Record<number, string>>({})

  const requestsQuery = useQuery({
    queryKey: ['developer-access-requests', 'pending'],
    queryFn: async () => {
      const response = await listDeveloperAccessRequests('pending')
      if (!response.success) {
        throw new Error(response.message || t('Unable to load unlock requests'))
      }
      return (response.data ?? []).filter(
        (request) => request.source !== 'legacy'
      )
    },
    retry: false,
    staleTime: 0,
    refetchInterval: (query) => (query.state.error ? false : 5_000),
    refetchIntervalInBackground: false,
  })
  const requests = requestsQuery.data ?? []
  const loading = requestsQuery.isFetching
  const errorStatus = (
    requestsQuery.error as { response?: { status?: number } } | null
  )?.response?.status
  const available = !(requestsQuery.isError && errorStatus === 404)

  useEffect(() => {
    if (!requestsQuery.isError || errorStatus === 404) return
    toast.error(
      requestsQuery.error instanceof Error
        ? requestsQuery.error.message
        : t('Unable to load unlock requests')
    )
  }, [errorStatus, requestsQuery.error, requestsQuery.isError, t])

  useEffect(() => {
    if (
      props.focusRequestId === undefined ||
      loading ||
      typeof document === 'undefined'
    ) {
      return
    }
    document
      .getElementById(`developer-access-request-${props.focusRequestId}`)
      ?.scrollIntoView({ block: 'center', behavior: 'smooth' })
  }, [loading, props.focusRequestId])

  const review = async (
    request: DeveloperAccessRequestAdmin,
    approve: boolean
  ) => {
    if (reviewing !== null) return
    const note = (notes[request.id] ?? '').trim()
    if ([...note].length < 2) {
      toast.error(t('Administrator reply must contain at least 2 characters.'))
      return
    }
    setReviewing(request.id)
    try {
      const response = await reviewDeveloperAccessRequest(
        request.id,
        approve ? 'approve' : 'reject',
        note
      )
      if (!response.success) {
        throw new Error(
          response.message || t('Unable to review unlock request')
        )
      }
      toast.success(
        approve ? t('Access request approved') : t('Access request rejected')
      )
      await requestsQuery.refetch()
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
            : t('Unable to review unlock request')
      )
      await requestsQuery.refetch()
    } finally {
      setReviewing(null)
    }
  }

  if (!available) return null

  return (
    <section className='border-border border-t pt-10'>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <div>
          <div className='flex items-center gap-2'>
            <h2 className='text-sm font-semibold'>{t('L1 access requests')}</h2>
            <Badge variant='secondary'>{requests.length}</Badge>
          </div>
          <p className='text-muted-foreground mt-1 text-sm'>
            {t(
              'Pending L0 requests appear here only when automatic review has not approved them. The assistant may also grant L1 directly after three completed turns.'
            )}
          </p>
        </div>
        <Button
          variant='outline'
          size='sm'
          onClick={() => void requestsQuery.refetch()}
          disabled={loading}
        >
          <RefreshCw
            data-icon='inline-start'
            className={loading ? 'animate-spin' : undefined}
          />
          {t('Refresh')}
        </Button>
      </div>

      {loading && requests.length === 0 ? (
        <p className='text-muted-foreground mt-5 text-sm'>{t('Loading...')}</p>
      ) : null}
      {!loading && requests.length === 0 ? (
        <p className='text-muted-foreground mt-5 text-sm'>
          {t('No pending unlock requests.')}
        </p>
      ) : null}
      {requests.length > 0 ? (
        <div className='mt-6'>
          {requests.map((request) => (
            <RequestCard
              key={request.id}
              request={request}
              reviewing={reviewing}
              notes={notes}
              onReview={review}
              onNoteChange={(id, value) =>
                setNotes((current) => ({ ...current, [id]: value }))
              }
            />
          ))}
        </div>
      ) : null}
    </section>
  )
}

function RequestCard(props: {
  request: DeveloperAccessRequestAdmin
  reviewing: number | null
  notes: Record<number, string>
  onReview: (
    request: DeveloperAccessRequestAdmin,
    approve: boolean
  ) => Promise<void>
  onNoteChange: (id: number, value: string) => void
}) {
  const { t } = useTranslation()
  const { request, reviewing, notes, onReview, onNoteChange } = props
  let sourceLabel = t('Direct request')
  if (request.source === 'assistant_recommendation') {
    sourceLabel = t('AI recommendation')
  } else if (request.source === 'user_edited') {
    sourceLabel = t('User-edited recommendation')
  } else if (request.source === 'assistant_request') {
    sourceLabel = t('Direct request')
  }

  return (
    <article
      id={`developer-access-request-${request.id}`}
      className='border-border border-b py-7'
    >
      <div className='flex flex-wrap items-start justify-between gap-3'>
        <div className='min-w-0'>
          <p className='font-medium'>{request.username}</p>
          <p className='text-muted-foreground text-xs'>
            {request.email || t('No email provided')} · #{request.id}
          </p>
        </div>
        <div className='flex flex-wrap items-center gap-2'>
          <Badge variant='outline'>{sourceLabel}</Badge>
          <Badge variant='outline'>{t('Pending review')}</Badge>
        </div>
      </div>
      <div className='mt-5 max-w-3xl'>
        <p className='text-xs font-medium'>{t('Recommendation letter')}</p>
        <p className='text-muted-foreground mt-1 text-sm whitespace-pre-wrap'>
          {request.ai_recommendation ||
            request.reason ||
            t('No reason provided.')}
        </p>
      </div>
      <Textarea
        className='mt-5 max-w-3xl rounded-xl'
        rows={2}
        required
        minLength={2}
        maxLength={2000}
        aria-invalid={
          [...(notes[request.id] ?? '').trim()].length > 0 &&
          [...(notes[request.id] ?? '').trim()].length < 2
        }
        placeholder={t('Required reply to the user (at least 2 characters)')}
        value={notes[request.id] ?? ''}
        onChange={(event) => onNoteChange(request.id, event.target.value)}
      />
      <div className='mt-3 flex flex-wrap justify-end gap-2'>
        <Button
          size='sm'
          variant='destructive'
          onClick={() => void onReview(request, false)}
          disabled={
            reviewing !== null ||
            [...(notes[request.id] ?? '').trim()].length < 2
          }
        >
          <X data-icon='inline-start' />
          {t('Reject')}
        </Button>
        <Button
          size='sm'
          onClick={() => void onReview(request, true)}
          disabled={
            reviewing !== null ||
            [...(notes[request.id] ?? '').trim()].length < 2
          }
        >
          <Check data-icon='inline-start' />
          {t('Approve and unlock L1')}
        </Button>
      </div>
    </article>
  )
}
