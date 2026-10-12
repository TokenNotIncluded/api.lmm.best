import { ImagePlus, SendHorizontal, Square, X } from 'lucide-react'
/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { useRef, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'

import type { RemoteCommandInput, RemoteKey } from './commands'
import { prepareRemoteImage, type RemoteImage } from './images'
import type { RemoteQuestion } from './types'

type Send = (command: RemoteCommandInput) => Promise<void>

export function RemoteQuestionCard({
  question,
  send,
  disabled,
}: {
  question: RemoteQuestion
  send: Send
  disabled: boolean
}) {
  const { t } = useTranslation()
  const [answer, setAnswer] = useState('')
  const [error, setError] = useState('')
  const [sending, setSending] = useState(false)
  const expired =
    question.expires_at !== undefined && question.expires_at <= Date.now()
  const blocked = disabled || sending || expired
  async function submit(command: RemoteCommandInput) {
    if (blocked) return
    setError('')
    setSending(true)
    try {
      await send(command)
      setAnswer('')
    } catch (cause) {
      setError(cause instanceof Error ? t(cause.message) : t('Command failed'))
    } finally {
      setSending(false)
    }
  }
  function respond(value: string | boolean | number) {
    void submit({
      action: 'ui_response',
      request_id: question.request_id,
      value,
    })
  }
  function key(value: RemoteKey) {
    void submit({
      action: 'ui_input',
      request_id: question.request_id,
      key: value,
    })
  }
  return (
    <section
      className='bg-primary/5 max-h-[32dvh] max-w-full min-w-0 shrink-0 space-y-3 overflow-y-auto rounded-lg p-4 sm:p-5'
      aria-label={t('Pi needs your answer')}
    >
      <p className='text-primary text-xs font-semibold'>
        {t('Pi needs your answer')}
      </p>
      {question.title ? (
        <h3 className='font-medium break-words'>{question.title}</h3>
      ) : null}
      <p className='text-sm break-words whitespace-pre-wrap'>
        {question.question}
      </p>
      {question.kind === 'select' ? (
        <div className='grid gap-2'>
          {question.options?.map((option, index) => (
            <Button
              key={index}
              type='button'
              variant='outline'
              disabled={blocked}
              className='h-auto min-h-11 justify-start text-left whitespace-normal'
              onClick={() => respond(index)}
            >
              {option}
            </Button>
          ))}
        </div>
      ) : question.kind === 'confirm' ? (
        <div className='flex gap-2'>
          <Button disabled={blocked} onClick={() => respond(true)}>
            {t('Confirm')}
          </Button>
          <Button
            variant='outline'
            disabled={blocked}
            onClick={() => respond(false)}
          >
            {t('Decline')}
          </Button>
        </div>
      ) : question.kind === 'custom' ? (
        <>
          <pre
            className='bg-background max-h-96 max-w-full min-w-0 overflow-auto rounded-xl p-3 text-xs whitespace-pre'
            aria-label={t('Pi terminal view')}
          >
            {question.content}
          </pre>
          <div
            className='flex flex-wrap gap-2'
            role='group'
            aria-label={t('Terminal controls')}
          >
            {(
              [
                ['up', '↑'],
                ['down', '↓'],
                ['left', '←'],
                ['right', '→'],
                ['enter', t('Enter')],
                ['escape', t('Cancel')],
                ['tab', 'Tab'],
                ['space', t('Space')],
                ['backspace', '⌫'],
                ['ctrl+s', t('Submit')],
              ] as [RemoteKey, string][]
            ).map(([value, label]) => (
              <Button
                type='button'
                key={value}
                variant='outline'
                className='min-h-11 min-w-11'
                aria-label={value}
                disabled={blocked}
                onClick={() => key(value)}
              >
                {label}
              </Button>
            ))}
          </div>
          <form
            className='flex min-w-0 gap-2'
            onSubmit={(event) => {
              event.preventDefault()
              void submit({
                action: 'ui_input',
                request_id: question.request_id,
                text: answer,
              })
            }}
          >
            <Input
              aria-label={t('Text for Pi')}
              value={answer}
              maxLength={2000}
              onChange={(event) => setAnswer(event.target.value)}
              disabled={blocked}
              autoComplete='off'
            />
            <Button type='submit' disabled={blocked || !answer}>
              {t('Insert text')}
            </Button>
          </form>
        </>
      ) : (
        <form
          className='space-y-2'
          onSubmit={(event) => {
            event.preventDefault()
            respond(answer)
          }}
        >
          <Textarea
            aria-label={t('Your answer')}
            value={answer}
            maxLength={8000}
            placeholder={question.placeholder}
            onChange={(event) => setAnswer(event.target.value)}
            disabled={blocked}
            autoComplete='off'
          />
          <Button type='submit' disabled={blocked}>
            {t('Send answer')}
          </Button>
        </form>
      )}
      {question.kind !== 'custom' ? (
        <Button
          type='button'
          variant='ghost'
          disabled={blocked}
          onClick={() =>
            void submit({
              action: 'ui_response',
              request_id: question.request_id,
              cancelled: true,
            })
          }
        >
          {t('Cancel question')}
        </Button>
      ) : null}
      {sending ? (
        <p role='status' className='text-muted-foreground text-xs'>
          {t('Waiting for Pi confirmation')}
        </p>
      ) : null}
      {error ? (
        <p role='alert' className='text-destructive text-sm'>
          {error}
        </p>
      ) : null}
    </section>
  )
}

export function RemoteComposer({
  send,
  disabled,
  busy,
  hasQuestion,
  supportsImages = false,
}: {
  send: Send
  disabled: boolean
  busy: boolean
  hasQuestion: boolean
  supportsImages?: boolean
}) {
  const { t } = useTranslation()
  const [content, setContent] = useState('')
  const picker = useRef<HTMLInputElement>(null)
  const [image, setImage] = useState<RemoteImage>()
  const [preparing, setPreparing] = useState(false)
  const blocked = disabled || preparing || hasQuestion
  async function attach(file: File) {
    if (blocked || !supportsImages || sending) return
    setPreparing(true)
    setError('')
    try {
      setImage(await prepareRemoteImage(file))
    } catch (cause) {
      setError(
        cause instanceof Error
          ? t(cause.message)
          : t('Unable to prepare this image. Choose a smaller image.')
      )
    } finally {
      setPreparing(false)
    }
  }
  const [delivery, setDelivery] = useState<'followUp' | 'steer'>('followUp')
  const [sending, setSending] = useState(false)
  const [status, setStatus] = useState('')
  const [error, setError] = useState('')
  async function submit(event: FormEvent) {
    event.preventDefault()
    if ((!content.trim() && !image) || sending || blocked) return
    setSending(true)
    setError('')
    setStatus('')
    try {
      await send({
        action: 'prompt',
        content,
        delivery,
        ...(image ? { images: [image] } : {}),
      })
      setContent('')
      setImage(undefined)
      setStatus(t('Pi accepted the task'))
    } catch (cause) {
      setError(cause instanceof Error ? t(cause.message) : t('Command failed'))
    } finally {
      setSending(false)
    }
  }
  async function stop() {
    setError('')
    setStatus('')
    try {
      await send({ action: 'abort' })
      setStatus(t('Pi accepted the stop request'))
    } catch (cause) {
      setError(cause instanceof Error ? t(cause.message) : t('Command failed'))
    }
  }
  return (
    <form
      className='bg-background sticky bottom-0 z-10 max-w-full min-w-0 shrink-0 space-y-3 border-t pt-4 pb-2'
      onSubmit={submit}
    >
      {image ? (
        <div className='relative w-fit rounded-lg border p-1'>
          <img
            src={`data:image/jpeg;base64,${image.data}`}
            alt={t('Image Preview')}
            className='max-h-40 max-w-full rounded object-contain'
          />
          <Button
            type='button'
            variant='secondary'
            size='icon'
            className='absolute top-1 right-1 min-h-11 min-w-11'
            aria-label={t('Remove attachment')}
            disabled={sending || preparing}
            onClick={() => setImage(undefined)}
          >
            <X className='size-4' aria-hidden='true' />
          </Button>
        </div>
      ) : null}
      <Textarea
        className='min-h-24 resize-y rounded-lg text-base'
        aria-label={t('Send a task to Pi')}
        placeholder={
          hasQuestion
            ? t('Answer the question above first')
            : t('Send a task to Pi')
        }
        value={content}
        maxLength={8000}
        onChange={(event) => setContent(event.target.value)}
        onPaste={(event) => {
          const file = Array.from(event.clipboardData.items)
            .find((item) => item.kind === 'file')
            ?.getAsFile()
          if (file && supportsImages && !blocked && !sending) {
            event.preventDefault()
            void attach(file)
          }
        }}
        disabled={blocked || sending}
        autoComplete='off'
      />
      <input
        ref={picker}
        type='file'
        accept='image/png,image/jpeg,image/webp'
        className='hidden'
        aria-label={t('Image input')}
        onChange={(event) => {
          const file = event.target.files?.[0]
          event.target.value = ''
          if (file) void attach(file)
        }}
      />
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div className='flex flex-wrap items-center gap-3'>
          <Button
            type='button'
            variant='outline'
            className='min-h-11'
            disabled={!supportsImages || blocked || sending}
            title={
              !supportsImages
                ? t('Update the Pi plugin to send images.')
                : undefined
            }
            onClick={() => picker.current?.click()}
          >
            <ImagePlus className='size-4' aria-hidden='true' />
            {preparing ? t('Loading...') : t('Image')}
          </Button>
          <label className='text-muted-foreground flex min-h-11 items-center gap-2 text-xs sm:text-sm'>
            <input
              type='checkbox'
              checked={delivery === 'steer'}
              disabled={blocked || sending}
              onChange={(event) =>
                setDelivery(event.target.checked ? 'steer' : 'followUp')
              }
            />
            {t('Steer the current task')}
          </label>
        </div>
        <div className='flex gap-2'>
          <Button
            type='button'
            variant='outline'
            className='min-h-11'
            onClick={() => void stop()}
            disabled={disabled || (!busy && !hasQuestion)}
          >
            <Square className='size-3' aria-hidden='true' />
            {t('Stop Pi')}
          </Button>
          <Button
            type='submit'
            className='min-h-11'
            disabled={blocked || sending || (!content.trim() && !image)}
          >
            <SendHorizontal className='size-4' aria-hidden='true' />
            {sending ? t('Waiting for Pi confirmation') : t('Send task')}
          </Button>
        </div>
      </div>
      {supportsImages ? (
        <p className='text-muted-foreground text-xs'>
          {t(
            'Paste or choose one image. It is resized locally before encrypted sending.'
          )}
        </p>
      ) : (
        <p className='text-muted-foreground text-xs'>
          {t('Update the Pi plugin to send images.')}
        </p>
      )}
      {status ? (
        <p role='status' className='text-muted-foreground text-sm'>
          {status}
        </p>
      ) : null}
      {error ? (
        <p role='alert' className='text-destructive text-sm'>
          {error}
        </p>
      ) : null}
    </form>
  )
}
