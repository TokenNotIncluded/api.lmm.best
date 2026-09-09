/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Textarea } from '@/components/ui/textarea'
import {
  acceptAssistantSupport,
  closeAssistantSupport,
  getAssistantSupport,
  sendAssistantSupportMessage,
} from '@/features/assistant/assistant-support-api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import type { TodoItem } from './api'

export function HumanSupportDialog({
  item,
  onClose,
}: {
  item: TodoItem
  onClose: () => void
}) {
  const user = useAuthStore((state) => state.auth.user)
  const session = useAuthStore((state) => state.auth.session?.sid)
  if (!user || user.role < ROLE.ADMIN) return null
  return (
    <HumanSupportDialogContent
      key={`${user.id}:${session ?? ''}:${item.source_id}`}
      item={item}
      userId={user.id}
      sessionId={session}
      onClose={onClose}
    />
  )
}

function HumanSupportDialogContent({
  item,
  userId,
  sessionId,
  onClose,
}: {
  item: TodoItem
  userId: number
  sessionId?: string
  onClose: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  const current = () => {
    const auth = useAuthStore.getState().auth
    return (
      mounted.current &&
      auth.user?.id === userId &&
      auth.session?.sid === sessionId &&
      auth.user.role >= ROLE.ADMIN
    )
  }
  const [accepted, setAccepted] = useState(
    item.details?.status === 'accepted' &&
      item.details?.assigned_admin_id === userId
  )
  const [content, setContent] = useState('')
  const queryKey = [
    'assistant-support-admin',
    userId,
    sessionId,
    item.source_id,
  ]
  const query = useQuery({
    queryKey,
    queryFn: () => getAssistantSupport(item.source_id),
    enabled: accepted,
    refetchInterval: 3_000,
    refetchIntervalInBackground: false,
    retry: false,
  })
  const refresh = () => {
    if (!current()) return
    void queryClient.invalidateQueries({ queryKey: ['todos'] })
    void queryClient.invalidateQueries({ queryKey })
  }
  const claim = useMutation({
    mutationFn: () => acceptAssistantSupport(item.source_id),
    onSuccess: () => {
      if (!current()) return
      setAccepted(true)
      refresh()
    },
    onError: refresh,
  })
  const send = useMutation({
    mutationFn: (message: string) =>
      sendAssistantSupportMessage(item.source_id, message),
    onSuccess: () => {
      if (!current()) return
      setContent('')
      refresh()
    },
    onError: refresh,
  })
  const complete = useMutation({
    mutationFn: () => closeAssistantSupport(item.source_id, false),
    onSuccess: () => {
      if (!current()) return
      refresh()
      onClose()
    },
    onError: refresh,
  })
  const request = query.data?.request
  const canReply =
    !query.isError &&
    request?.status === 'accepted' &&
    request.assigned_admin_id === userId
  const busy = claim.isPending || send.isPending || complete.isPending
  const error = claim.error ?? send.error ?? complete.error
  const preferredTime = request?.preferred_time ?? item.details?.preferred_time
  const scheduledAt = request?.scheduled_at ?? item.details?.scheduled_at
  const topic = request?.topic ?? item.summary

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className='flex max-h-[90dvh] flex-col gap-5 sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle>{t('Human technical support')}</DialogTitle>
          <DialogDescription>
            {t('Accept this request to join the user’s existing conversation.')}
          </DialogDescription>
        </DialogHeader>
        <div className='space-y-2 text-sm'>
          <p className='font-medium'>{topic}</p>
          <p className='text-muted-foreground'>
            {t('User ID')} {item.details?.user_id as number}
            {typeof item.details?.username === 'string'
              ? ` · ${item.details.username}`
              : ''}
          </p>
          {typeof scheduledAt === 'number' && scheduledAt > 0 ? (
            <p className='text-muted-foreground'>
              {t('Requested support time')}:{' '}
              {new Date(scheduledAt * 1000).toLocaleString()}
              {typeof preferredTime === 'string' && preferredTime
                ? ` · ${preferredTime}`
                : ''}
            </p>
          ) : null}
        </div>
        {error ? (
          <p role='alert' className='text-destructive text-sm'>
            {error.message}
          </p>
        ) : null}
        {!accepted ? (
          <Button
            onClick={() => claim.mutate()}
            disabled={busy || item.details?.user_id === userId}
          >
            {t('Accept support request')}
          </Button>
        ) : query.isError ? (
          <div role='alert' className='space-y-2 text-sm'>
            <p>
              {t('Support conversation unavailable. Refresh to check access.')}
            </p>
            <Button variant='outline' onClick={() => void query.refetch()}>
              {t('Retry')}
            </Button>
          </div>
        ) : query.isPending ? (
          <p>{t('Loading')}</p>
        ) : (
          <>
            <div
              role='log'
              aria-label={t('Support conversation')}
              className='min-h-24 flex-1 space-y-5 overflow-y-auto border-y py-4'
            >
              {query.data?.messages.map((message) => (
                <div key={message.id} className='space-y-1'>
                  <p className='text-muted-foreground text-xs'>
                    {message.role === 'user'
                      ? t('User')
                      : message.role === 'human'
                        ? message.actor_name || t('Human technical support')
                        : t('AI assistant')}
                  </p>
                  <p className='text-sm break-words whitespace-pre-wrap'>
                    {message.content}
                  </p>
                </div>
              ))}
            </div>
            {canReply ? (
              <form
                onSubmit={(event) => {
                  event.preventDefault()
                  if (content.trim() && !busy) send.mutate(content.trim())
                }}
                className='space-y-3'
              >
                <Textarea
                  aria-label={t('Reply to user')}
                  placeholder={t('Reply to user')}
                  maxLength={8_000}
                  value={content}
                  onChange={(event) => setContent(event.target.value)}
                  disabled={busy}
                />
                <div className='flex items-center justify-between gap-3'>
                  <Button
                    type='button'
                    variant='outline'
                    disabled={busy}
                    onClick={() => complete.mutate()}
                  >
                    {t('End human support')}
                  </Button>
                  <Button type='submit' disabled={busy || !content.trim()}>
                    {t('Send')}
                  </Button>
                </div>
              </form>
            ) : (
              <p className='text-muted-foreground text-sm'>
                {t('This support request has ended.')}
              </p>
            )}
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}
