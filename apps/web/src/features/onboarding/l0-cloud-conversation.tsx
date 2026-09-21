/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import {
  type RefObject,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import { useTranslation } from 'react-i18next'

import { Response } from '@/components/ai-elements/response'
import { sendAssistantMessage } from '@/features/assistant/api'
import { requestAssistantOpen } from '@/features/assistant/assistant-events'
import {
  hasAssistantMessageSubstantialMeaning,
  redactAssistantMessageForRequest,
} from '@/features/assistant/assistant-message-safety'
import { getAssistantPromptValidation } from '@/features/assistant/assistant-prompt-validation'
import { useAuthStore } from '@/stores/auth-store'

import { getL0AccessCopy } from './l0-access-copy'
import { createL0ChatSession } from './l0-chat-session'
import { mountL0TextFlow, visualTokens, type L0TextFlow } from './l0-text-flow'

export function L0CloudConversation({ cloudRef }: {
  cloudRef: RefObject<HTMLDivElement | null>
}) {
  const { t, i18n } = useTranslation()
  const copy = getL0AccessCopy(i18n.resolvedLanguage || i18n.language)
  const [prompt, setPrompt] = useState('')
  const composing = useRef(false)
  const committed = useRef('')
  const input = useRef<HTMLInputElement>(null)
  const answer = useRef<HTMLDivElement>(null)
  const pane = useRef<HTMLElement>(null)
  const follow = useRef(true)
  const flow = useRef<L0TextFlow | null>(null)
  const session = useMemo(() => createL0ChatSession(
    async ({ message, history, conversationId, turnId }, handlers, signal) => {
      const auth = useAuthStore.getState().auth
      if (!auth.user) throw new Error('Authentication required')
      const sameAccount = () => {
        const latest = useAuthStore.getState().auth
        return latest.user?.id === auth.user?.id && latest.session?.sid === auth.session?.sid
      }
      const reply = await sendAssistantMessage(
        message, history, conversationId, undefined,
        {
          onDelta: (delta) => { if (sameAccount()) handlers.onDelta(delta) },
          onReset: () => { if (sameAccount()) handlers.onReset() },
        },
        signal, false, turnId
      )
      if (!sameAccount()) throw new Error('Account changed')
      return { ...reply, needsAction: Boolean(reply.action || reply.supportRequest) }
    },
    (text) => redactAssistantMessageForRequest(text).content
  ), [])
  const [state, setState] = useState(session.snapshot)
  const [formatted, setFormatted] = useState(false)
  const busy = state.phase === 'waiting' || state.phase === 'streaming'
  const valid = prompt.trim().length > 0 && !getAssistantPromptValidation(prompt, true).invalid
  const presets = [t('Help me choose a model'), t('Connect my coding tools')]
  const tokens = useMemo(() => visualTokens(state.answer), [state.answer])
  // Keep a bounded animated tail; a long answer stays complete and selectable.
  const tail = tokens.slice(-192)
  const prefix = state.answer.slice(0, tail[0]?.index ?? 0)

  useEffect(() => session.subscribe(setState), [session])
  useLayoutEffect(() => {
    if (!cloudRef.current) return
    const mounted = mountL0TextFlow(cloudRef.current)
    flow.current = mounted
    return () => { mounted.dispose(); flow.current = null }
  }, [cloudRef])
  useLayoutEffect(() => {
    flow.current?.clearResponses()
    setFormatted(false)
  }, [state.revision])
  useLayoutEffect(() => {
    if (follow.current && pane.current) pane.current.scrollTop = pane.current.scrollHeight
    if (answer.current) flow.current?.receive(answer.current)
  }, [state.answer, state.revision])
  useEffect(() => {
    if (state.phase !== 'done' && state.phase !== 'stopped' && state.phase !== 'error') return
    // Only the final markdown swap waits for landing, never the network or next request.
    const timer = setTimeout(() => {
      flow.current?.clear()
      setFormatted(true)
    }, 360)
    return () => clearTimeout(timer)
  }, [state.phase, state.answer])

  const ask = (text: string, source?: HTMLElement) => {
    if (composing.current || getAssistantPromptValidation(text, true).invalid) return
    const safe = redactAssistantMessageForRequest(text).content.trim()
    if (!hasAssistantMessageSubstantialMeaning(safe) || !session.send(safe)) return
    flow.current?.clear()
    if (source) flow.current?.sentence(source)
    else if (input.current) flow.current?.input(input.current)
    committed.current = ''
    setPrompt('')
  }
  const type = (element: HTMLInputElement) => {
    setPrompt(element.value)
    if (composing.current) return
    flow.current?.input(element, committed.current)
    committed.current = element.value
  }

  return (
    <div className='l0-composer' data-phase={state.phase}>
      <h2 id='l0-welcome-title' className={state.phase === 'idle' ? '' : 'l0-sr-only'}>
        {t('Your next idea starts here.')}
      </h2>
      {state.phase !== 'idle' && (
        <section ref={pane} className='l0-dialogue' aria-label={copy.conversation}
          onScroll={(event) => {
            const node = event.currentTarget
            follow.current = node.scrollHeight - node.scrollTop - node.clientHeight < 48
          }}>
          <div className='l0-dialogue-heading'>
            <p className='l0-question-echo'>{state.question}</p>
            <button type='button' className='l0-clear-chat' aria-label={copy.newChat}
              disabled={busy} onClick={() => { flow.current?.clear(); session.clear(); input.current?.focus() }}>
              <svg viewBox='0 0 24 24' fill='none' aria-hidden='true'><path d='M6 12h12M12 6v12' /></svg>
            </button>
          </div>
          <div ref={answer} className='l0-answer' aria-live='off'>
            {formatted ? <Response>{state.answer}</Response> : <>
              {prefix}
              {tail.map((token) => (
                <span key={token.index} data-l0-arrival>{token.text}</span>
              ))}
            </>}
          </div>
          <p className='l0-sr-only' role='status'>
            {busy ? copy.responding : state.phase === 'error' ? copy.chatError : state.answer}
          </p>
          {state.phase === 'waiting' && <div className='l0-await' aria-hidden='true'><i /><i /><i /></div>}
          {state.phase === 'error' && <p role='alert' className='l0-chat-notice'>{copy.chatError}</p>}
          {state.phase === 'stopped' && <p className='l0-chat-notice'>{copy.stopped}</p>}
          {state.needsAction && (
            <button className='l0-next-action' type='button' onClick={() => requestAssistantOpen(undefined, state.question)}>
              {copy.continueAction} ↗
            </button>
          )}
        </section>
      )}
      <form className='l0-input-row' onSubmit={(event) => { event.preventDefault(); ask(prompt) }}>
        <label htmlFor='l0-question' className='l0-sr-only'>{t('What would you like to do?')}</label>
        <span className='l0-input-mark' aria-hidden='true'>{'>'}</span>
        <input ref={input} id='l0-question' value={prompt}
          onChange={(event) => type(event.currentTarget)}
          onCompositionStart={() => { composing.current = true }}
          onCompositionEnd={(event) => { composing.current = false; type(event.currentTarget) }}
          onKeyDown={(event) => {
            if (event.key === 'Enter' && (composing.current || event.nativeEvent.isComposing || event.keyCode === 229)) event.preventDefault()
          }}
          maxLength={4000} placeholder={t('Describe your idea or ask a question')}
          aria-describedby='l0-privacy' autoComplete='off'
        />
        {busy ? (
          <button type='button' className='l0-send' aria-label={copy.stop}
            onClick={() => { session.stop(); flow.current?.clear() }}>
            <svg viewBox='0 0 24 24' fill='none' aria-hidden='true'><rect x='7' y='7' width='10' height='10' rx='1' /></svg>
          </button>
        ) : (
          <button type='submit' className='l0-send' disabled={!valid} aria-label={t('Ask AI assistant')}>
            <svg viewBox='0 0 24 24' fill='none' aria-hidden='true'><path d='M12 19V5m-6 6 6-6 6 6' /></svg>
          </button>
        )}
      </form>
      <p id='l0-privacy' className='l0-sr-only'>{t('Never paste a password, API key, session cookie, or other secret into the conversation.')}</p>
      <div className='l0-shortcuts'>
        {presets.map((preset) => (
          <button key={preset} type='button' data-l0-preset disabled={busy}
            aria-label={preset} onClick={(event) => ask(preset, event.currentTarget)}>
            {visualTokens(preset).map((token) => <span key={token.index} data-l0-source>{token.text}</span>)}
          </button>
        ))}
      </div>
    </div>
  )
}
