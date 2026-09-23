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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { formatDateTimeObject } from '@/lib/time'
import { useAuthStore } from '@/stores/auth-store'

import {
  developerAccessRequestQueryKey,
  getDeveloperAccessRequest,
  submitDeveloperAccessRequest,
} from './api'

export function AccessRequestDetails({ inline = false }: { inline?: boolean }) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const client = useQueryClient()
  const [editing, setEditing] = useState(false)
  const [reason, setReason] = useState<string | null>(null)
  const queryKey = developerAccessRequestQueryKey(user?.id ?? 0)
  const query = useQuery({
    queryKey,
    queryFn: async () => {
      const request = await getDeveloperAccessRequest()
      return request ? { ...request } : null
    },
    enabled: !!user,
    staleTime: 15_000,
    retry: false,
  })
  const mutation = useMutation({
    mutationFn: (input: {
      userId: number
      reason: string
      recommendation?: string
    }) =>
      submitDeveloperAccessRequest({
        reason: input.reason,
        confirmed: true,
        ...(input.recommendation
          ? { ai_recommendation: input.recommendation }
          : {}),
      }),
    onSuccess: (request, input) => {
      client.setQueryData(developerAccessRequestQueryKey(input.userId), request)
      setEditing(false)
      setReason(null)
    },
  })
  if (!user) return null
  const request = query.data
  if (query.isPending) {
    return <p className='text-muted-foreground text-sm'>{t('Loading')}</p>
  }
  if (query.isError) {
    return (
      <div className='space-y-2'>
        <p role='alert'>{t('Unable to load access status')}</p>
        <Button
          type='button'
          variant='outline'
          onClick={() => void query.refetch()}
          disabled={query.isFetching}
        >
          {t('Retry')}
        </Button>
      </div>
    )
  }
  const canEdit =
    user.developer_access_granted !== true && request?.status !== 'approved'
  const draft = reason ?? request?.reason ?? ''
  const reasonLength = Array.from(draft.trim()).length
  const showForm =
    canEdit && (editing || (inline && request?.status !== 'pending'))
  const Container = inline ? 'section' : 'details'
  return (
    <Container
      className='space-y-3 text-sm'
      {...(!inline ? { open: editing || undefined } : {})}
      data-testid={inline ? 'l0-direct-access-request' : undefined}
    >
      {!inline && (
        <summary className='cursor-pointer'>
          {t('View access request status')}
        </summary>
      )}
      {inline && request?.status === 'pending' && (
        <p data-testid='l0-pending-request'>
          {t(
            request.ai_recommendation
              ? 'AI recommendation submitted'
              : 'Access request submitted'
          )}
        </p>
      )}
      {request ? (
        <dl className='space-y-3'>
          <div>
            <dt className='text-muted-foreground'>{t('Status')}</dt>
            <dd>
              {t(
                request.status === 'pending'
                  ? 'Pending review'
                  : request.status === 'approved'
                    ? 'Access request approved'
                    : 'Access request rejected'
              )}
            </dd>
          </div>
          <div>
            <dt className='text-muted-foreground'>
              {t('Original request submitted')}
            </dt>
            <dd>{formatDateTimeObject(new Date(request.created_at * 1000))}</dd>
          </div>
          {request.reviewed_at > 0 && (
            <div>
              <dt className='text-muted-foreground'>{t('Review completed')}</dt>
              <dd>
                {formatDateTimeObject(new Date(request.reviewed_at * 1000))}
              </dd>
            </div>
          )}
          <div>
            <dt className='text-muted-foreground'>{t('Reason')}</dt>
            <dd className='break-words whitespace-pre-wrap'>
              {request.reason}
            </dd>
          </div>
          {request.ai_recommendation && (
            <div>
              <dt className='text-muted-foreground'>
                {t('AI recommendation')}
              </dt>
              <dd className='break-words whitespace-pre-wrap'>
                {request.ai_recommendation}
              </dd>
            </div>
          )}
          {request.admin_note && (
            <div>
              <dt className='text-muted-foreground'>
                {t('Administrator note')}
              </dt>
              <dd className='break-words whitespace-pre-wrap'>
                {request.admin_note}
              </dd>
            </div>
          )}
        </dl>
      ) : (
        <p>{t('Not requested')}</p>
      )}
      {canEdit && !showForm && (
        <Button
          type='button'
          variant='outline'
          onClick={() => {
            setReason(request?.reason ?? '')
            mutation.reset()
            setEditing(true)
          }}
        >
          {t(request ? 'Revise access request' : 'Request API access')}
        </Button>
      )}
      {showForm && (
        <form
          className='space-y-3'
          onSubmit={(event) => {
            event.preventDefault()
            if (mutation.isPending || reasonLength < 5 || reasonLength > 2000) {
              return
            }
            mutation.mutate({
              userId: user.id,
              reason: draft.trim(),
              recommendation: request?.ai_recommendation,
            })
          }}
        >
          <Label htmlFor='access-request-reason'>{t('Reason')}</Label>
          <Textarea
            id='access-request-reason'
            value={draft}
            onChange={(event) => setReason(event.target.value)}
            disabled={mutation.isPending}
            rows={5}
            className='text-base sm:text-sm'
            aria-describedby='access-request-reason-help'
            aria-invalid={
              reason !== null && (reasonLength < 5 || reasonLength > 2000)
            }
          />
          <p
            id='access-request-reason-help'
            className='text-muted-foreground text-xs'
          >
            {t(
              'Describe your intended API use in 5–2000 characters. Do not include credentials.'
            )}
          </p>
          {request && (
            <p className='text-muted-foreground text-xs'>
              {t(
                'Submitting replaces this application and sends it for review again. The previous review note is cleared; the AI recommendation is retained.'
              )}
            </p>
          )}
          {mutation.isError && (
            <p role='alert'>
              {t(
                'Could not submit the access request. Your text is preserved; check the status and try again.'
              )}
            </p>
          )}
          <div className='flex flex-wrap gap-2'>
            <Button
              type='submit'
              disabled={
                mutation.isPending || reasonLength < 5 || reasonLength > 2000
              }
            >
              {t('Confirm and submit application')}
            </Button>
            {editing && (
              <Button
                type='button'
                variant='ghost'
                disabled={mutation.isPending}
                onClick={() => setEditing(false)}
              >
                {t('Cancel')}
              </Button>
            )}
          </div>
        </form>
      )}
    </Container>
  )
}
