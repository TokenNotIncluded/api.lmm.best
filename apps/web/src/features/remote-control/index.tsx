/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'
import {
  AlertCircle,
  ChevronDown,
  Folder,
  LockKeyhole,
  MonitorCog,
  RefreshCw,
} from 'lucide-react'
import { type FormEvent, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Markdown } from '@/components/ui/markdown'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { api } from '@/lib/api'
import { formatTimestampToDate } from '@/lib/format'

import {
  decryptPiRemoteMessage,
  decryptPiSessionMetadata,
  derivePiRemoteKey,
} from './crypto'
import {
  isCollapsedMessage,
  normalizePiMessageEnvelopes,
  normalizePiSessionEnvelopes,
} from './protocol'
import type {
  PiMessagesResponse,
  PiRemoteSession,
  PiRemoteSessionEnvelope,
  PiSessionsResponse,
  RemoteControlMessage,
} from './types'

function displayTime(value: string | number | undefined) {
  if (value === undefined) return null
  const parsed = typeof value === 'number' ? value : Date.parse(value)
  if (!Number.isFinite(parsed)) return String(value)
  const milliseconds = parsed < 10_000_000_000 ? parsed * 1000 : parsed
  return formatTimestampToDate(milliseconds, 'milliseconds')
}

function MessageBody({ message }: { message: RemoteControlMessage }) {
  const body =
    message.content ||
    (message.arguments === undefined
      ? ''
      : `\`\`\`json\n${JSON.stringify(message.arguments, null, 2)}\n\`\`\``)
  if (message.type === 'ask_user') {
    return (
      <div className='space-y-3'>
        <Markdown>{message.question || body}</Markdown>
        {message.options?.length ? (
          <div className='flex flex-wrap gap-2'>
            {message.options.map((option) => (
              <span key={option} className='border px-3 py-1.5 text-sm'>
                {option}
              </span>
            ))}
          </div>
        ) : null}
      </div>
    )
  }
  return body ? <Markdown>{body}</Markdown> : null
}

export function SessionMessages({
  messages,
}: {
  messages: RemoteControlMessage[]
}) {
  const { t } = useTranslation()
  if (!messages.length) {
    return <p className='text-muted-foreground text-sm'>{t('No messages')}</p>
  }
  return (
    <div className='space-y-3'>
      {messages.map((message, index) => {
        const title =
          message.title ||
          message.tool_name ||
          (message.type === 'thinking'
            ? t('Thinking')
            : message.type === 'ask_user'
              ? t('Question')
              : message.type.replaceAll('_', ' '))
        if (isCollapsedMessage(message.type)) {
          return (
            <details
              key={message.id || `${message.type}-${index}`}
              className='border p-3'
            >
              <summary className='flex cursor-pointer list-none items-center justify-between gap-3 text-sm font-medium'>
                <span>{title}</span>
                <ChevronDown className='size-4' aria-hidden='true' />
              </summary>
              <div className='mt-3 border-t pt-3'>
                <MessageBody message={message} />
              </div>
            </details>
          )
        }
        return (
          <div
            key={message.id || `${message.type}-${index}`}
            className='border p-3'
          >
            <div className='text-muted-foreground mb-2 text-xs font-medium uppercase'>
              {title}
            </div>
            <MessageBody message={message} />
          </div>
        )
      })}
    </div>
  )
}

function SessionDetail({
  session,
  sessionKey,
  unlockRevision,
  onLock,
}: {
  session: PiRemoteSession
  sessionKey: CryptoKey
  unlockRevision: number
  onLock: () => void
}) {
  const { t } = useTranslation()
  const messagesQuery = useQuery({
    queryKey: ['remote-control', 'pi', 'messages', session.id, unlockRevision],
    queryFn: async () => {
      const response = await api.get<PiMessagesResponse>(
        `/api/remote-control/v1/pi/sessions/${encodeURIComponent(session.id)}/messages?after=0`,
        { skipBusinessError: true }
      )
      if (!response.data.success) {
        throw new Error(response.data.message || 'Unable to load messages')
      }
      return Promise.all(
        normalizePiMessageEnvelopes(response.data.data).map((message) =>
          decryptPiRemoteMessage(session.id, message, sessionKey)
        )
      )
    },
    refetchInterval: 2_500,
    gcTime: 0,
  })
  const messages = [...session.messages, ...(messagesQuery.data ?? [])]

  return (
    <div className='min-w-0 space-y-4'>
      <div className='flex min-w-0 items-center justify-between gap-3'>
        <div className='min-w-0'>
          <h2 className='truncate text-sm font-semibold'>
            {session.summary || session.id}
          </h2>
          <p className='text-muted-foreground truncate text-xs'>
            {session.deviceId}
          </p>
          {session.directory ? (
            <p className='text-muted-foreground mt-1 flex min-w-0 items-center gap-1 text-xs'>
              <Folder className='size-3 shrink-0' aria-hidden='true' />
              <span className='truncate'>{session.directory}</span>
            </p>
          ) : null}
        </div>
        <Button type='button' variant='outline' size='sm' onClick={onLock}>
          <LockKeyhole data-icon='inline-start' />
          {t('Lock')}
        </Button>
      </div>
      <div className='grid gap-3 sm:grid-cols-2'>
        <div className='border p-3'>
          <div className='text-muted-foreground text-xs'>ID</div>
          <code className='text-sm break-all'>{session.id}</code>
        </div>
        <div className='border p-3'>
          <div className='text-muted-foreground text-xs'>{t('Runtime')}</div>
          <div className='text-sm'>{session.runtime || t('Unknown')}</div>
        </div>
        <div className='border p-3 sm:col-span-2'>
          <div className='text-muted-foreground text-xs'>{t('Started')}</div>
          <div className='text-sm'>
            {displayTime(session.startedAt) || t('Unknown')}
          </div>
        </div>
      </div>
      {session.summary ? <Markdown>{session.summary}</Markdown> : null}
      {messagesQuery.isPending ? (
        <p className='text-muted-foreground text-sm'>{t('Loading...')}</p>
      ) : messagesQuery.isError ? (
        <Alert variant='destructive'>
          <AlertCircle aria-hidden='true' />
          <AlertTitle>{t('Unable to decrypt messages')}</AlertTitle>
          <AlertDescription>
            {t('Check the PIN or wait for the Pi session to reconnect.')}
          </AlertDescription>
          <Button
            className='mt-2 w-fit'
            type='button'
            size='sm'
            variant='outline'
            onClick={() => void messagesQuery.refetch()}
          >
            <RefreshCw data-icon='inline-start' />
            {t('Retry')}
          </Button>
        </Alert>
      ) : (
        <SessionMessages messages={messages} />
      )}
    </div>
  )
}

function SessionWorkspace({ envelope }: { envelope: PiRemoteSessionEnvelope }) {
  const { t } = useTranslation()
  const [pin, setPin] = useState('')
  const [unlocking, setUnlocking] = useState(false)
  const [unlockError, setUnlockError] = useState(false)
  const [unlocked, setUnlocked] = useState<{
    key: CryptoKey
    session: PiRemoteSession
    revision: number
  } | null>(null)

  async function unlock(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!pin || unlocking) return
    setUnlocking(true)
    setUnlockError(false)
    try {
      const key = await derivePiRemoteKey(pin, envelope.sessionId)
      const session = await decryptPiSessionMetadata(envelope, key)
      setUnlocked({ key, session, revision: Date.now() })
      setPin('')
    } catch {
      setUnlockError(true)
    } finally {
      setUnlocking(false)
    }
  }

  if (unlocked) {
    return (
      <SessionDetail
        session={unlocked.session}
        sessionKey={unlocked.key}
        unlockRevision={unlocked.revision}
        onLock={() => setUnlocked(null)}
      />
    )
  }

  return (
    <div className='border p-4 sm:p-5'>
      <div className='mb-5 flex items-start gap-3'>
        <div className='bg-muted flex size-9 shrink-0 items-center justify-center rounded-md'>
          <LockKeyhole className='size-4' aria-hidden='true' />
        </div>
        <div className='min-w-0'>
          <h2 className='text-sm font-semibold'>{t('Unlock Pi session')}</h2>
          <p className='text-muted-foreground mt-1 text-sm text-pretty'>
            {t(
              'Enter the PIN shown by Pi. It stays in this browser tab and is never sent to the server.'
            )}
          </p>
        </div>
      </div>
      <form className='max-w-sm space-y-3' onSubmit={unlock} autoComplete='off'>
        <div className='space-y-1.5'>
          <Label htmlFor={`pi-pin-${envelope.sessionId}`}>{t('PIN')}</Label>
          <Input
            id={`pi-pin-${envelope.sessionId}`}
            type='password'
            value={pin}
            onChange={(event) => setPin(event.target.value)}
            maxLength={128}
            autoComplete='off'
            autoCapitalize='none'
            spellCheck={false}
            aria-invalid={unlockError}
            aria-describedby={
              unlockError ? `pi-pin-error-${envelope.sessionId}` : undefined
            }
            autoFocus
          />
          {unlockError ? (
            <p
              id={`pi-pin-error-${envelope.sessionId}`}
              className='text-destructive text-sm'
              role='alert'
            >
              {t('The PIN is incorrect or this session uses another protocol.')}
            </p>
          ) : null}
        </div>
        <Button type='submit' disabled={!pin || unlocking}>
          <LockKeyhole data-icon='inline-start' />
          {unlocking ? t('Unlocking...') : t('Unlock')}
        </Button>
      </form>
    </div>
  )
}

export function RemoteControl() {
  const { t } = useTranslation()
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const sessionsQuery = useQuery({
    queryKey: ['remote-control', 'pi', 'sessions'],
    queryFn: async () => {
      const response = await api.get<PiSessionsResponse>(
        '/api/remote-control/v1/pi/sessions',
        { skipBusinessError: true }
      )
      if (!response.data.success) {
        throw new Error(response.data.message || 'Unable to load sessions')
      }
      return normalizePiSessionEnvelopes(response.data.data)
    },
    refetchInterval: 15_000,
  })
  const sessions = sessionsQuery.data ?? []
  const selected =
    sessions.find((session) => session.sessionId === selectedId) ?? sessions[0]

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Remote control')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='mx-auto w-full max-w-6xl pb-16'>
          <Tabs defaultValue='pi'>
            <TabsList aria-label={t('Runtime')}>
              <TabsTrigger value='pi'>Pi</TabsTrigger>
            </TabsList>
            <TabsContent value='pi' className='pt-4'>
              <div className='grid gap-6 lg:grid-cols-[minmax(16rem,0.36fr)_minmax(0,1fr)]'>
                <div className='space-y-2'>
                  {sessionsQuery.isPending ? (
                    <p className='text-muted-foreground text-sm'>
                      {t('Loading...')}
                    </p>
                  ) : sessionsQuery.isError ? (
                    <p className='text-destructive text-sm'>
                      {t('Failed to load')}
                    </p>
                  ) : sessions.length === 0 ? (
                    <p className='text-muted-foreground text-sm'>
                      {t('No active sessions')}
                    </p>
                  ) : (
                    sessions.map((session) => (
                      <button
                        key={session.sessionId}
                        type='button'
                        onClick={() => setSelectedId(session.sessionId)}
                        className={`focus-visible:border-ring focus-visible:ring-ring/50 w-full border p-3 text-left outline-none focus-visible:ring-3 ${selected?.sessionId === session.sessionId ? 'bg-muted' : ''}`}
                      >
                        <div className='flex items-center gap-2 text-sm font-medium'>
                          <MonitorCog className='size-4' aria-hidden='true' />
                          <span className='truncate'>{session.deviceId}</span>
                        </div>
                        <div className='text-muted-foreground mt-2 space-y-1 text-xs'>
                          <div>
                            {displayTime(session.updatedAt) || t('Unknown')}
                          </div>
                          <div className='truncate'>{session.sessionId}</div>
                        </div>
                      </button>
                    ))
                  )}
                </div>
                {selected ? (
                  <SessionWorkspace
                    key={selected.sessionId}
                    envelope={selected}
                  />
                ) : null}
              </div>
            </TabsContent>
          </Tabs>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

export type { PiRemoteSession, RemoteControlMessage } from './types'
