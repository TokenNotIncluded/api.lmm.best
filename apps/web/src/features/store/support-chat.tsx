/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, Send, Sparkles } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { requestAssistantOpen } from '@/features/assistant/assistant-events'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { StoreError, StoreLoading } from './shared'
import { supportApi, type SupportAssistantContext } from './support-api'
import {
  storeSupportAssistantPrompt,
  supportMessageAttempt,
  type SupportAssistantTask,
  type SupportDraft,
} from './support-helpers'
import { storeDate } from './utils'

type DraftUpdate = SupportDraft | ((current: SupportDraft) => SupportDraft)

export function StoreSupportChat({
  userId,
  id,
  draft,
  onDraft,
  onBack,
}: {
  userId: number
  id: string
  draft: SupportDraft
  onDraft: (id: string, update: DraftUpdate) => void
  onBack: () => void
}) {
  const { t, i18n } = useTranslation()
  const client = useQueryClient()
  const [cursors, setCursors] = useState([0])
  const before = cursors.at(-1) ?? 0
  const [busy, setBusy] = useState(false)
  const sending = useRef(false)
  const [error, setError] = useState<unknown>(null)
  const [assistant, setAssistant] = useState<SupportAssistantContext | null>(
    null
  )
  const [assistantBusy, setAssistantBusy] = useState(false)
  const [task, setTask] = useState<SupportAssistantTask>('reply')
  const [preview, setPreview] = useState('')
  const alive = useRef(true)
  const assistantAbort = useRef<AbortController | null>(null)
  const panel = useRef<HTMLDivElement>(null)
  const lastElement = useRef<HTMLDivElement>(null)
  const nearBottom = useRef(true)
  const acknowledged = useRef(0)
  const marking = useRef(false)
  const query = useQuery({
    queryKey: ['store-support', userId, 'history', id, before],
    queryFn: ({ signal }) => supportApi.history(id, before, signal),
    refetchInterval: before === 0 ? 5000 : false,
    refetchIntervalInBackground: false,
    gcTime: 0,
    retry: false,
  })
  const conversation = query.data?.conversation
  const last = query.data?.items.at(-1)
  const ownRead = conversation
    ? conversation.buyer_id === userId
      ? conversation.buyer_read_id
      : conversation.seller_read_id
    : 0
  const otherRead = conversation
    ? conversation.buyer_id === userId
      ? conversation.seller_read_id
      : conversation.buyer_read_id
    : 0

  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
      assistantAbort.current?.abort()
    }
  }, [])
  useEffect(() => {
    if (before === 0 && nearBottom.current && panel.current) {
      panel.current.scrollTop = panel.current.scrollHeight
    }
  }, [last?.id, before])
  useEffect(() => {
    const element = lastElement.current
    const through = last?.id
    if (
      !element ||
      !through ||
      through <= Math.max(ownRead, acknowledged.current) ||
      typeof IntersectionObserver === 'undefined'
    ) {
      return
    }
    let visible = false
    const mark = () => {
      if (
        !visible ||
        document.visibilityState !== 'visible' ||
        marking.current ||
        through <= acknowledged.current ||
        useAuthStore.getState().auth.user?.id !== userId
      ) {
        return
      }
      marking.current = true
      void supportApi
        .markRead(id, through)
        .then(() => {
          acknowledged.current = Math.max(acknowledged.current, through)
          void client.invalidateQueries({
            queryKey: ['store-support', userId, 'threads'],
          })
        })
        .catch(() => {
          // A read receipt failure must not discard a draft or claim success.
        })
        .finally(() => {
          marking.current = false
        })
    }
    const observer = new IntersectionObserver(
      ([entry]) => {
        visible = entry.isIntersecting
        mark()
      },
      { threshold: 0.5 }
    )
    observer.observe(element)
    document.addEventListener('visibilitychange', mark)
    return () => {
      observer.disconnect()
      document.removeEventListener('visibilitychange', mark)
    }
  }, [id, userId, last?.id, ownRead, client])

  async function refresh() {
    await Promise.all([
      client.invalidateQueries({
        queryKey: ['store-support', userId, 'history', id],
      }),
      client.invalidateQueries({
        queryKey: ['store-support', userId, 'threads'],
      }),
    ])
  }
  async function send() {
    if (sending.current || useAuthStore.getState().auth.user?.id !== userId) {
      return
    }
    sending.current = true
    setBusy(true)
    setError(null)
    try {
      const attempt = supportMessageAttempt(draft)
      onDraft(id, (current) => ({ ...current, attempt }))
      await supportApi.send(id, attempt.body, attempt.key)
      if (useAuthStore.getState().auth.user?.id !== userId) return
      onDraft(id, (current) =>
        current.attempt?.key === attempt.key &&
        current.text.trim() === attempt.body
          ? { text: '' }
          : current
      )
      if (alive.current) {
        setCursors([0])
        nearBottom.current = true
      }
      await refresh()
    } catch (issue) {
      if (alive.current) setError(issue)
    } finally {
      sending.current = false
      if (alive.current) setBusy(false)
    }
  }
  async function changeStatus() {
    if (!conversation || busy) return
    setBusy(true)
    setError(null)
    try {
      await supportApi.status(
        id,
        conversation.status === 'resolved' ? 'open' : 'resolved'
      )
      await refresh()
    } catch (issue) {
      if (alive.current) setError(issue)
    } finally {
      if (alive.current) setBusy(false)
    }
  }
  async function prepareAssistant() {
    assistantAbort.current?.abort()
    const abort = new AbortController()
    assistantAbort.current = abort
    setAssistantBusy(true)
    setError(null)
    try {
      const context = await supportApi.assistantContext(id, abort.signal)
      if (
        !alive.current ||
        abort.signal.aborted ||
        useAuthStore.getState().auth.user?.id !== userId
      ) {
        return
      }
      setAssistant(context)
      setTask('reply')
      setPreview(storeSupportAssistantPrompt(context, 'reply', i18n.language))
    } catch (issue) {
      if (alive.current && !abort.signal.aborted) setError(issue)
    } finally {
      if (alive.current && !abort.signal.aborted) setAssistantBusy(false)
    }
  }
  return (
    <section
      className='bg-muted/20 min-w-0 space-y-4 rounded-3xl p-4 sm:p-6'
      aria-label={t('Conversation')}
    >
      <header className='flex min-w-0 flex-wrap items-center justify-between gap-3'>
        <div className='flex min-w-0 items-center gap-2'>
          <Button
            className='min-h-11 shrink-0 md:hidden'
            variant='ghost'
            aria-label={t('Back to conversations')}
            onClick={onBack}
          >
            <ArrowLeft className='size-5' />
          </Button>
          <div className='min-w-0'>
            <h2 className='truncate font-semibold'>
              {conversation?.subject || t('Conversation')}
            </h2>
            {conversation?.order_id && (
              <a
                href={`/store/orders?order=${encodeURIComponent(conversation.order_id)}`}
                className='text-muted-foreground inline-flex min-h-11 items-center text-xs underline underline-offset-4'
              >
                {t('View order')}
              </a>
            )}
          </div>
        </div>
        {conversation && (
          <Button
            className='min-h-11 rounded-full'
            size='sm'
            variant='ghost'
            disabled={busy}
            onClick={() => void changeStatus()}
          >
            {t(
              conversation.status === 'resolved'
                ? 'Reopen conversation'
                : 'Mark resolved'
            )}
          </Button>
        )}
      </header>
      <StoreError error={query.error} retry={() => void query.refetch()} />
      <StoreError error={error} />
      {query.isPending ? (
        <StoreLoading />
      ) : (
        query.data && (
          <>
            <div className='flex flex-wrap justify-between gap-2'>
              <Button
                size='sm'
                variant='ghost'
                className='min-h-11'
                disabled={!query.data.has_more || query.isFetching}
                onClick={() => {
                  const first = query.data?.items[0]
                  if (first) setCursors((current) => [...current, first.id])
                }}
              >
                {t('Earlier messages')}
              </Button>
              {before > 0 && (
                <Button
                  size='sm'
                  variant='ghost'
                  className='min-h-11'
                  onClick={() => {
                    setCursors((current) => current.slice(0, -1))
                    nearBottom.current = true
                  }}
                >
                  {t('Newer messages')}
                </Button>
              )}
            </div>
            <div
              ref={panel}
              role='log'
              aria-label={t('Messages')}
              aria-live='polite'
              aria-relevant='additions'
              className='h-[40dvh] min-h-56 space-y-4 overflow-y-auto overscroll-contain px-1 py-2'
              onScroll={() => {
                if (panel.current) {
                  nearBottom.current =
                    panel.current.scrollHeight -
                      panel.current.scrollTop -
                      panel.current.clientHeight <
                    100
                }
              }}
            >
              {query.data.items.length === 0 && (
                <p className='text-muted-foreground py-12 text-center text-sm'>
                  {t(
                    'Start with your question. The other party can reply here.'
                  )}
                </p>
              )}
              {query.data.items.map((message, index) => (
                <div
                  key={message.id}
                  ref={
                    index === query.data.items.length - 1
                      ? lastElement
                      : undefined
                  }
                  className={cn(
                    'flex',
                    message.sender_id === userId
                      ? 'justify-end'
                      : 'justify-start'
                  )}
                >
                  <div
                    className={cn(
                      'max-w-[90%] space-y-2 rounded-2xl px-4 py-3 text-sm sm:max-w-[85%]',
                      message.sender_id === userId
                        ? 'bg-primary/10'
                        : 'bg-background'
                    )}
                  >
                    <p className='text-muted-foreground text-xs'>
                      {t(
                        message.sender_id === userId
                          ? 'You'
                          : message.sender_id === conversation?.seller_id
                            ? 'Seller'
                            : 'Buyer'
                      )}
                    </p>
                    <p className='[overflow-wrap:anywhere] whitespace-pre-wrap'>
                      {message.body}
                    </p>
                    <p className='text-muted-foreground text-xs'>
                      {storeDate(message.created_at, i18n.language)}
                      {message.sender_id === userId &&
                        ` · ${t(otherRead >= message.id ? 'Read' : 'Sent')}`}
                    </p>
                  </div>
                </div>
              ))}
            </div>
            <form
              className='space-y-3'
              onSubmit={(event) => {
                event.preventDefault()
                void send()
              }}
            >
              <label htmlFor={`support-message-${id}`} className='sr-only'>
                {t('Message')}
              </label>
              <Textarea
                id={`support-message-${id}`}
                value={draft.text}
                maxLength={4000}
                rows={3}
                className='bg-background min-h-24 resize-y rounded-2xl'
                placeholder={t(
                  'Write a message. Never share API keys or pickup codes.'
                )}
                disabled={busy}
                onChange={(event) =>
                  onDraft(id, { ...draft, text: event.target.value })
                }
                onKeyDown={(event) => {
                  if (
                    (event.ctrlKey || event.metaKey) &&
                    event.key === 'Enter' &&
                    !event.nativeEvent.isComposing
                  ) {
                    event.preventDefault()
                    void send()
                  }
                }}
              />
              <div className='flex flex-wrap items-center justify-between gap-2'>
                <Button
                  type='button'
                  variant='ghost'
                  className='min-h-11 rounded-full'
                  disabled={assistantBusy}
                  onClick={() => void prepareAssistant()}
                >
                  <Sparkles className='size-4' aria-hidden='true' />
                  {t('Assistant help')}
                </Button>
                <Button
                  type='submit'
                  className='min-h-11 rounded-full px-6'
                  disabled={busy || !draft.text.trim()}
                >
                  <Send className='size-4' aria-hidden='true' />
                  {t('Send message')}
                </Button>
              </div>
              {conversation?.status === 'resolved' && (
                <p className='text-muted-foreground text-xs'>
                  {t('Sending a message reopens this conversation.')}
                </p>
              )}
            </form>
          </>
        )
      )}
      {assistant && (
        <section
          className='bg-background space-y-3 rounded-2xl p-4'
          aria-label={t('Review assistant context')}
        >
          <h3 className='font-medium'>{t('Review assistant context')}</h3>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Only the latest ten messages and basic order status are included. Private notes and delivery data are excluded. Check the text before opening the assistant.'
            )}
          </p>
          <div className='flex flex-wrap gap-1'>
            {(['reply', 'summary', 'help'] as const).map((value) => (
              <Button
                key={value}
                size='sm'
                className='min-h-11 rounded-full'
                variant={task === value ? 'secondary' : 'ghost'}
                aria-pressed={task === value}
                onClick={() => {
                  setTask(value)
                  setPreview(
                    storeSupportAssistantPrompt(assistant, value, i18n.language)
                  )
                }}
              >
                {t(
                  value === 'reply'
                    ? 'Draft a reply'
                    : value === 'summary'
                      ? 'Summarize conversation'
                      : 'Suggest next steps'
                )}
              </Button>
            ))}
          </div>
          <Textarea
            aria-label={t('Text to share with the assistant')}
            value={preview}
            onChange={(event) => setPreview(event.target.value)}
            rows={8}
            maxLength={12000}
          />
          <p className='text-muted-foreground text-xs'>
            {t(
              'The assistant does not send a reply to the other party. Review its draft before copying it into your message.'
            )}
          </p>
          <div className='flex flex-wrap gap-2'>
            <Button
              className='min-h-11 rounded-full'
              disabled={!preview.trim()}
              onClick={() => requestAssistantOpen('service', preview)}
            >
              {t('Open in assistant')}
            </Button>
            <Button
              variant='ghost'
              className='min-h-11 rounded-full'
              onClick={() => {
                setAssistant(null)
                setPreview('')
              }}
            >
              {t('Close')}
            </Button>
          </div>
        </section>
      )}
    </section>
  )
}
