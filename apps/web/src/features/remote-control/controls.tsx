/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import type { RemoteCommandInput, RemoteKey } from './commands'
import type { RemoteQuestion } from './types'

type Send = (command: RemoteCommandInput) => Promise<void>

export function RemoteQuestionCard({ question, send, disabled }: { question: RemoteQuestion; send: Send; disabled: boolean }) {
  const { t } = useTranslation()
  const [answer, setAnswer] = useState('')
  const [error, setError] = useState('')
  const [sending, setSending] = useState(false)
  const expired = question.expires_at !== undefined && question.expires_at <= Date.now()
  const blocked = disabled || sending || expired
  async function submit(command: RemoteCommandInput) {
    if (blocked) return
    setError('')
    setSending(true)
    try { await send(command); setAnswer('') }
    catch (cause) { setError(cause instanceof Error ? t(cause.message) : t('Command failed')) }
    finally { setSending(false) }
  }
  function respond(value: string | boolean | number) {
    void submit({ action: 'ui_response', request_id: question.request_id, value })
  }
  function key(value: RemoteKey) {
    void submit({ action: 'ui_input', request_id: question.request_id, key: value })
  }
  return (
    <section className='bg-primary/5 min-w-0 max-w-full space-y-3 rounded-2xl p-4 sm:p-5' aria-label={t('Pi needs your answer')}>
      <p className='text-primary text-xs font-semibold'>{t('Pi needs your answer')}</p>
      {question.title ? <h3 className='font-medium break-words'>{question.title}</h3> : null}
      <p className='whitespace-pre-wrap text-sm break-words'>{question.question}</p>
      {question.kind === 'select' ? (
        <div className='grid gap-2'>
          {question.options?.map((option, index) => (
            <Button key={index} type='button' variant='outline' disabled={blocked} className='h-auto min-h-11 justify-start whitespace-normal text-left' onClick={() => respond(index)}>{option}</Button>
          ))}
        </div>
      ) : question.kind === 'confirm' ? (
        <div className='flex gap-2'>
          <Button disabled={blocked} onClick={() => respond(true)}>{t('Confirm')}</Button>
          <Button variant='outline' disabled={blocked} onClick={() => respond(false)}>{t('Decline')}</Button>
        </div>
      ) : question.kind === 'custom' ? (
        <>
          <pre className='bg-background min-w-0 max-h-96 max-w-full overflow-auto rounded-xl p-3 text-xs whitespace-pre' aria-label={t('Pi terminal view')}>{question.content}</pre>
          <div className='flex flex-wrap gap-2' role='group' aria-label={t('Terminal controls')}>
            {([['up', '↑'], ['down', '↓'], ['left', '←'], ['right', '→'], ['enter', t('Enter')], ['escape', t('Cancel')], ['tab', 'Tab'], ['space', t('Space')], ['backspace', '⌫'], ['ctrl+s', t('Submit')]] as [RemoteKey, string][]).map(([value, label]) => (
              <Button type='button' key={value} variant='outline' className='min-h-11 min-w-11' aria-label={value} disabled={blocked} onClick={() => key(value)}>{label}</Button>
            ))}
          </div>
          <form className='flex min-w-0 gap-2' onSubmit={(event) => { event.preventDefault(); void submit({ action: 'ui_input', request_id: question.request_id, text: answer }) }}>
            <Input aria-label={t('Text for Pi')} value={answer} maxLength={2000} onChange={(event) => setAnswer(event.target.value)} disabled={blocked} autoComplete='off' />
            <Button type='submit' disabled={blocked || !answer}>{t('Insert text')}</Button>
          </form>
        </>
      ) : (
        <form className='space-y-2' onSubmit={(event) => { event.preventDefault(); respond(answer) }}>
          <Textarea aria-label={t('Your answer')} value={answer} maxLength={8000} placeholder={question.placeholder} onChange={(event) => setAnswer(event.target.value)} disabled={blocked} autoComplete='off' />
          <Button type='submit' disabled={blocked}>{t('Send answer')}</Button>
        </form>
      )}
      {question.kind !== 'custom' ? <Button type='button' variant='ghost' disabled={blocked} onClick={() => void submit({ action: 'ui_response', request_id: question.request_id, cancelled: true })}>{t('Cancel question')}</Button> : null}
      {sending ? <p role='status' className='text-muted-foreground text-xs'>{t('Waiting for Pi confirmation')}</p> : null}
      {error ? <p role='alert' className='text-destructive text-sm'>{error}</p> : null}
    </section>
  )
}

export function RemoteComposer({ send, disabled, busy, hasQuestion }: { send: Send; disabled: boolean; busy: boolean; hasQuestion: boolean }) {
  const { t } = useTranslation()
  const [content, setContent] = useState('')
  const [delivery, setDelivery] = useState<'followUp' | 'steer'>('followUp')
  const [sending, setSending] = useState(false)
  const [status, setStatus] = useState('')
  const [error, setError] = useState('')
  async function submit(event: FormEvent) {
    event.preventDefault()
    if (!content.trim() || sending || disabled || hasQuestion) return
    setSending(true); setError(''); setStatus('')
    try { await send({ action: 'prompt', content, delivery }); setContent(''); setStatus(t('Pi accepted the task')) }
    catch (cause) { setError(cause instanceof Error ? t(cause.message) : t('Command failed')) }
    finally { setSending(false) }
  }
  async function stop() {
    setError(''); setStatus('')
    try { await send({ action: 'abort' }); setStatus(t('Pi accepted the stop request')) }
    catch (cause) { setError(cause instanceof Error ? t(cause.message) : t('Command failed')) }
  }
  return (
    <form className='bg-muted/40 min-w-0 max-w-full space-y-3 rounded-2xl p-4' onSubmit={submit}>
      <Textarea aria-label={t('Send a task to Pi')} placeholder={hasQuestion ? t('Answer the question above first') : t('Send a task to Pi')} value={content} maxLength={8000} onChange={(event) => setContent(event.target.value)} disabled={disabled || sending || hasQuestion} autoComplete='off' />
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <label className='text-muted-foreground flex items-center gap-2 text-sm'>
          <input type='checkbox' checked={delivery === 'steer'} onChange={(event) => setDelivery(event.target.checked ? 'steer' : 'followUp')} />
          {t('Steer the current task')}
        </label>
        <div className='flex gap-2'>
          <Button type='button' variant='outline' onClick={() => void stop()} disabled={disabled || (!busy && !hasQuestion)}>{t('Stop Pi')}</Button>
          <Button type='submit' disabled={disabled || sending || hasQuestion || !content.trim()}>{sending ? t('Waiting for Pi confirmation') : t('Send task')}</Button>
        </div>
      </div>
      {status ? <p role='status' className='text-muted-foreground text-sm'>{status}</p> : null}
      {error ? <p role='alert' className='text-destructive text-sm'>{error}</p> : null}
    </form>
  )
}
