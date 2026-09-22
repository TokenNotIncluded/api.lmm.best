/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'
import { Copy, Check, ArrowDown, RotateCcw } from 'lucide-react'
import {
  memo,
  type RefObject,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import { useTranslation } from 'react-i18next'

import { Response } from '@/components/ai-elements/response'
import {
  getAssistantStatus,
  getAssistantPreConversationPresets,
  sendAssistantMessage,
} from '@/features/assistant/api'
import { requestAssistantOpen } from '@/features/assistant/assistant-events'
import {
  hasAssistantMessageSubstantialMeaning,
  redactAssistantMessageForRequest,
} from '@/features/assistant/assistant-message-safety'
import {
  ASSISTANT_PROMPT_PRESET_COPY_VERSION,
  filterAssistantPreConversationPresets,
  localizeAssistantPreConversationPresets,
} from '@/features/assistant/assistant-prompt-presets'
import { getAssistantPromptValidation } from '@/features/assistant/assistant-prompt-validation'
import { useAuthStore } from '@/stores/auth-store'

import { getL0AccessCopy } from './l0-access-copy'
import { createL0ChatSession, type CloudTurn } from './l0-chat-session'
import {
  mountL0TextFlow,
  visualTokens,
  visualTokenTail,
  type L0TextFlow,
} from './l0-text-flow'

const PreviousTurn = memo(function PreviousTurn({ turn }: { turn: CloudTurn }) {
  return (
    <article className='l0-history-turn' data-testid='l0-history-turn'>
      <p className='l0-question-echo'>{turn.question}</p>
      <div className='l0-answer'>
        <Response>{turn.answer}</Response>
      </div>
    </article>
  )
})

export function L0CloudConversation({
  cloudRef,
  active = true,
}: {
  active?: boolean
  cloudRef: RefObject<HTMLDivElement | null>
}) {
  const { t, i18n } = useTranslation()
  const copy = getL0AccessCopy(i18n.resolvedLanguage || i18n.language)
  const user = useAuthStore((state) => state.auth.user)
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  const language = i18n.resolvedLanguage || i18n.language
  const statusQuery = useQuery({
    queryKey: ['assistant-status', user?.id, sessionId],
    queryFn: getAssistantStatus,
    enabled: active && Boolean(user),
    staleTime: 30_000,
    retry: false,
  })
  const available =
    statusQuery.data?.enabled !== false &&
    statusQuery.data?.route_available !== false
  const presetQuery = useQuery({
    queryKey: [
      'assistant-pre-conversation-presets',
      ASSISTANT_PROMPT_PRESET_COPY_VERSION,
      language,
      user?.id,
      sessionId,
    ],
    queryFn: () => getAssistantPreConversationPresets(language),
    enabled: active && Boolean(user) && available,
    staleTime: 5 * 60_000,
    retry: false,
  })
  const [prompt, setPrompt] = useState('')
  const [away, setAway] = useState(false)
  const [copyState, setCopyState] = useState<'copied' | 'copyFailed' | null>(
    null
  )
  const copyTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const alive = useRef(false)
  const composing = useRef(false)
  const committed = useRef('')
  const input = useRef<HTMLInputElement>(null)
  const answer = useRef<HTMLDivElement>(null)
  const pane = useRef<HTMLElement>(null)
  const follow = useRef(true)
  const flow = useRef<L0TextFlow | null>(null)
  const session = useMemo(
    () =>
      createL0ChatSession(
        async (
          { message, history, conversationId, turnId, presetId, replay },
          handlers,
          signal
        ) => {
          const auth = useAuthStore.getState().auth
          if (!auth.user) throw new Error('Authentication required')
          const sameAccount = () => {
            const latest = useAuthStore.getState().auth
            return (
              latest.user?.id === auth.user?.id &&
              latest.session?.sid === auth.session?.sid
            )
          }
          const reply = await sendAssistantMessage(
            message,
            history,
            conversationId,
            presetId,
            {
              onDelta: (delta) => {
                if (sameAccount()) handlers.onDelta(delta)
              },
              onReset: () => {
                if (sameAccount()) handlers.onReset()
              },
            },
            signal,
            replay ?? false,
            turnId
          )
          if (!sameAccount()) throw new Error('Account changed')
          return {
            ...reply,
            needsAction: Boolean(reply.action || reply.supportRequest),
          }
        },
        (text) => redactAssistantMessageForRequest(text).content
      ),
    []
  )
  const [state, setState] = useState(session.snapshot)
  const [formatted, setFormatted] = useState(false)
  const busy = state.phase === 'waiting' || state.phase === 'streaming'
  const valid =
    prompt.trim().length > 0 &&
    !getAssistantPromptValidation(prompt, true).invalid
  const presets = localizeAssistantPreConversationPresets(
    filterAssistantPreConversationPresets(presetQuery.data?.presets, user),
    t
  )
  const tail = useMemo(() => visualTokenTail(state.answer), [state.answer])
  const prefix = state.answer.slice(0, tail[0]?.index ?? 0)

  useEffect(() => session.subscribe(setState), [session])
  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
      clearTimeout(copyTimer.current)
    }
  }, [])
  useEffect(() => {
    if (!available) session.stop()
  }, [available, session])
  const toLatest = () => {
    follow.current = true
    setAway(false)
    if (pane.current) pane.current.scrollTop = pane.current.scrollHeight
  }
  const copyResponse = async () => {
    clearTimeout(copyTimer.current)
    try {
      await navigator.clipboard.writeText(state.answer)
      if (alive.current) setCopyState('copied')
    } catch {
      if (alive.current) setCopyState('copyFailed')
    }
    if (alive.current) {
      copyTimer.current = setTimeout(() => setCopyState(null), 1600)
    }
  }
  useLayoutEffect(() => {
    if (!cloudRef.current) return
    const mounted = mountL0TextFlow(cloudRef.current)
    flow.current = mounted
    return () => {
      mounted.dispose()
      flow.current = null
    }
  }, [cloudRef])
  useLayoutEffect(() => {
    flow.current?.clearResponses()
    setFormatted(false)
  }, [state.revision])
  useLayoutEffect(() => {
    if (active && follow.current && pane.current) {
      pane.current.scrollTop = pane.current.scrollHeight
    }
    if (answer.current) flow.current?.receive(answer.current, active)
  }, [state.answer, state.revision, active, formatted])
  useLayoutEffect(() => {
    if (!active) flow.current?.clear()
  }, [active])
  useEffect(() => {
    if (
      state.phase !== 'done' &&
      state.phase !== 'stopped' &&
      state.phase !== 'error'
    ) {
      return
    }
    // Only the final markdown swap waits for landing, never the network or next request.
    const timer = setTimeout(() => {
      flow.current?.clear()
      setFormatted(true)
    }, 360)
    return () => clearTimeout(timer)
  }, [state.phase, state.answer])

  const ask = (text: string, source?: HTMLElement, presetId?: string) => {
    if (
      !active ||
      !available ||
      !user ||
      composing.current ||
      getAssistantPromptValidation(text, true).invalid
    ) {
      return
    }
    const safe = redactAssistantMessageForRequest(text).content.trim()
    if (
      !hasAssistantMessageSubstantialMeaning(safe) ||
      !session.send(safe, presetId)
    ) {
      return
    }
    follow.current = true
    setAway(false)
    setCopyState(null)
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
      <h2
        id='l0-welcome-title'
        className={state.phase === 'idle' ? '' : 'l0-sr-only'}
      >
        {copy.greeting}
      </h2>
      {state.phase !== 'idle' && (
        <div className='l0-chat-toolbar'>
          <span>{copy.conversation}</span>
          <div>
            {away && (
              <button
                type='button'
                className='l0-jump-latest'
                onClick={toLatest}
                aria-label={copy.latest}
                title={copy.latest}
              >
                <ArrowDown aria-hidden='true' />
              </button>
            )}
            <button
              type='button'
              aria-label={copyState ? copy[copyState] : copy.copyAnswer}
              title={copyState ? copy[copyState] : copy.copyAnswer}
              disabled={!state.answer}
              onClick={() => void copyResponse()}
            >
              {copyState === 'copied' ? (
                <Check aria-hidden='true' />
              ) : (
                <Copy aria-hidden='true' />
              )}
            </button>
            <button
              type='button'
              className='l0-clear-chat'
              aria-label={copy.newChat}
              title={copy.newChat}
              disabled={busy}
              onClick={() => {
                flow.current?.clear()
                session.clear()
                follow.current = true
                setAway(false)
                input.current?.focus()
              }}
            >
              <svg viewBox='0 0 24 24' fill='none' aria-hidden='true'>
                <path d='M6 12h12M12 6v12' />
              </svg>
            </button>
          </div>
        </div>
      )}
      {state.phase !== 'idle' && (
        <section
          ref={pane}
          className='l0-dialogue'
          aria-label={copy.conversation}
          tabIndex={0}
          onScroll={(event) => {
            const node = event.currentTarget
            follow.current =
              node.scrollHeight - node.scrollTop - node.clientHeight < 48
            setAway(!follow.current)
          }}
        >
          {state.turns.map((turn) => (
            <PreviousTurn key={turn.id} turn={turn} />
          ))}
          <article className='l0-current-turn' data-testid='l0-current-turn'>
            <p className='l0-question-echo'>{state.question}</p>
            <div ref={answer} className='l0-answer' aria-live='off'>
              {formatted ? (
                <Response>{state.answer}</Response>
              ) : (
                <>
                  {prefix}
                  {tail.map((token) => (
                    <span key={token.index} data-l0-arrival>
                      {token.text}
                    </span>
                  ))}
                </>
              )}
            </div>
            <p className='l0-sr-only' role='status'>
              {busy
                ? copy.responding
                : state.phase === 'error'
                  ? copy.chatError
                  : state.answer}
            </p>
            {state.phase === 'waiting' && (
              <div className='l0-await' aria-hidden='true'>
                <i />
                <i />
                <i />
              </div>
            )}
            {state.phase === 'error' && (
              <p role='alert' className='l0-chat-notice'>
                {copy.chatError}
              </p>
            )}
            {state.phase === 'stopped' && (
              <p className='l0-chat-notice'>{copy.stopped}</p>
            )}
            {(state.phase === 'error' || state.phase === 'stopped') &&
              available &&
              state.question && (
                <button
                  type='button'
                  className='l0-next-action'
                  onClick={() => {
                    follow.current = true
                    setAway(false)
                    flow.current?.clear()
                    session.retry()
                  }}
                >
                  <RotateCcw aria-hidden='true' /> {copy.retry}
                </button>
              )}
            {state.needsAction && (
              <button
                className='l0-next-action'
                type='button'
                onClick={() => requestAssistantOpen(undefined, state.question)}
              >
                {copy.continueAction} ↗
              </button>
            )}
          </article>
        </section>
      )}
      <div className='l0-composer-dock'>
        <form
          className='l0-input-row'
          onSubmit={(event) => {
            event.preventDefault()
            ask(prompt)
          }}
        >
          <label htmlFor='l0-question' className='l0-sr-only'>
            {t('What would you like to do?')}
          </label>
          <span className='l0-input-mark' aria-hidden='true'>
            {'>'}
          </span>
          <input
            ref={input}
            id='l0-question'
            value={prompt}
            disabled={!available}
            onChange={(event) => type(event.currentTarget)}
            onCompositionStart={() => {
              composing.current = true
            }}
            onCompositionEnd={(event) => {
              composing.current = false
              type(event.currentTarget)
            }}
            onKeyDown={(event) => {
              if (
                event.key === 'Enter' &&
                (composing.current ||
                  event.nativeEvent.isComposing ||
                  event.keyCode === 229)
              ) {
                event.preventDefault()
              }
            }}
            maxLength={4000}
            placeholder={copy.prompt}
            aria-describedby='l0-privacy'
            autoComplete='off'
          />
          {busy ? (
            <button
              type='button'
              className='l0-send'
              aria-label={copy.stop}
              onClick={() => {
                session.stop()
                flow.current?.clear()
              }}
            >
              <svg viewBox='0 0 24 24' fill='none' aria-hidden='true'>
                <rect x='7' y='7' width='10' height='10' rx='1' />
              </svg>
            </button>
          ) : (
            <button
              type='submit'
              className='l0-send'
              disabled={!valid || !available}
              aria-label={t('Ask AI assistant')}
            >
              <svg viewBox='0 0 24 24' fill='none' aria-hidden='true'>
                <path d='M12 19V5m-6 6 6-6 6 6' />
              </svg>
            </button>
          )}
        </form>
        <p id='l0-privacy' className='l0-sr-only'>
          {t(
            'Never paste a password, API key, session cookie, or other secret into the conversation.'
          )}
        </p>
        {!available && (
          <p className='l0-chat-notice' role='status'>
            {copy.unavailable}
          </p>
        )}
        <span className='l0-sr-only' role='status'>
          {copyState ? copy[copyState] : null}
        </span>
        <div className='l0-shortcuts'>
          {presets.map((preset) => (
            <button
              key={preset.id}
              type='button'
              data-l0-preset
              disabled={busy || !available}
              aria-label={preset.label || preset.prompt}
              onClick={(event) =>
                ask(preset.prompt, event.currentTarget, preset.id)
              }
            >
              {visualTokens(preset.label || preset.prompt).map((token) => (
                <span key={token.index} data-l0-source>
                  {token.text}
                </span>
              ))}
            </button>
          ))}
        </div>
      </div>
    </div>
  )
}
