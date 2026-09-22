/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import {
  lazy,
  Suspense,
  type KeyboardEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
} from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { SourceQuestionnaire } from '@/features/acquisition/source-questionnaire'
import {
  requestAssistantOpen,
  peekQueuedAssistantRequest,
  consumeQueuedAssistantRequest,
  subscribeToAssistantOpen,
  type QueuedAssistantRequest,
} from '@/features/assistant/assistant-events'
import { PiOAuthGuide } from '@/features/guide/pi-oauth-guide'
import { toIntlLocale } from '@/i18n/languages'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'

import {
  developerAccessRequestQueryKey,
  getDeveloperAccessRequest,
} from './api'
import { getL0AccessCopy } from './l0-access-copy'
import { L0CloudConversation } from './l0-cloud-conversation'
import { getL0PaidAccess } from './l0-paid-access'
import {
  createL0Tokens,
  mountL0TokenCloud,
  projectL0Token,
} from './l0-token-cloud'
import { useL0AccessCheck } from './use-l0-access-check'

import './l0-welcome.css'

const AssistantTaskPanel = lazy(() =>
  import('@/features/assistant/assistant-panel').then((module) => ({
    default: module.AssistantPanel,
  }))
)

const SCENES = ['chat', 'explore', 'access'] as const
type Scene = (typeof SCENES)[number]
const FALLBACK_TOKENS = createL0Tokens(390).filter(
  (_, index) => index % 4 === 0
)

function Arrow({ diagonal = false }: { diagonal?: boolean }) {
  return (
    <svg viewBox='0 0 24 24' fill='none' aria-hidden='true'>
      <path d={diagonal ? 'M6 18 18 6M6 6h12v12' : 'M5 12h14m-5-5 5 5-5 5'} />
    </svg>
  )
}

function SceneIcon({ scene }: { scene: Scene }) {
  return (
    <svg viewBox='0 0 24 24' fill='none' aria-hidden='true'>
      {scene === 'chat' ? (
        <path d='M5 5h14v11H9l-4 4V5Zm4 5h6' />
      ) : scene === 'explore' ? (
        <>
          <circle cx='12' cy='12' r='8' />
          <path d='m15 9-2 4-4 2 2-4 4-2Z' />
        </>
      ) : (
        <>
          <rect x='5' y='10' width='14' height='10' rx='3' />
          <path d='M8 10V7a4 4 0 0 1 8 0M12 14v2' />
        </>
      )}
    </svg>
  )
}

function closeDisclosure(event: KeyboardEvent<HTMLDetailsElement>) {
  if (event.key !== 'Escape' || !event.currentTarget.open) return
  event.preventDefault()
  event.stopPropagation()
  event.currentTarget.open = false
  event.currentTarget.querySelector('summary')?.focus()
}

export function L0Welcome(props: {
  user: AuthUser | null
  children: ReactNode
}) {
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  return <L0WelcomeStage key={`${props.user?.id}:${sessionId}`} {...props} />
}

function L0WelcomeStage({
  user,
  children,
}: {
  user: AuthUser | null
  children: ReactNode
}) {
  const { t, i18n } = useTranslation()
  const navigate = useNavigate()
  const [scene, setScene] = useState<Scene>('chat')
  const [taskRequest, setTaskRequest] = useState<QueuedAssistantRequest | null>(
    null
  )
  const [taskRevision, setTaskRevision] = useState(0)
  const [consumedRequestId, setConsumedRequestId] = useState<string>()
  const [discovery, setDiscovery] = useState(0)
  const tabs = useRef<Array<HTMLButtonElement | null>>([])
  const helpMenu = useRef<HTMLDetailsElement>(null)
  const cloudRef = useRef<HTMLDivElement>(null)
  const { state: checkState, check } = useL0AccessCheck(user?.id)
  const request = useQuery({
    queryKey: developerAccessRequestQueryKey(user?.id ?? 0),
    queryFn: getDeveloperAccessRequest,
    enabled: false,
  })
  const language = i18n.resolvedLanguage || i18n.language
  const copy = getL0AccessCopy(language)
  const access = getL0PaidAccess(user)
  const canTopUp = access.mode === 'topup'
  const busy = checkState === 'checking'
  const statusLabel = request.isError
    ? t('Unable to load access status')
    : request.data?.status === 'pending'
      ? t('Pending review')
      : request.data?.status === 'rejected'
        ? t('Access request rejected')
        : request.data?.status === 'approved'
          ? t('Access request approved')
          : t('Account and access')
  const status = request.isError ? 'error' : request.data?.status || 'default'
  const money = (value: number) =>
    new Intl.NumberFormat(toIntlLocale(language), {
      style: 'currency',
      currency: 'USD',
      maximumFractionDigits: 6,
    }).format(value)
  const destinations = [
    {
      to: '/pricing',
      title: copy.models,
      note: copy.modelsNote,
      action: copy.browseModels,
      mark: '[]',
    },
    {
      to: '/tool-market',
      title: copy.tools,
      note: copy.toolsNote,
      action: copy.browseTools,
      mark: '/>',
    },
    {
      to: '/challenges',
      title: copy.challenges,
      note: copy.challengesNote,
      action: copy.browseChallenges,
      mark: '{}',
    },
  ] as const
  const selected = destinations[discovery]
  const labels = {
    chat: copy.conversation,
    explore: copy.explore,
    access: copy.access,
  }

  useEffect(() => {
    if (cloudRef.current) return mountL0TokenCloud(cloudRef.current)
  }, [])

  useEffect(() => {
    const openTask = (request: QueuedAssistantRequest) => {
      helpMenu.current?.removeAttribute('open')
      setTaskRequest(request)
      setTaskRevision((revision) => revision + 1)
      if (!request.autoSend) consumeQueuedAssistantRequest(request.id)
    }
    const queued = peekQueuedAssistantRequest()
    if (queued) openTask(queued)
    return subscribeToAssistantOpen(openTask)
  }, [])

  const returnToConversation = useCallback(() => {
    setTaskRequest(null)
    setScene('chat')
    window.requestAnimationFrame(() => {
      document.getElementById('l0-question')?.focus()
    })
  }, [])

  useEffect(() => {
    const focusConversation = (event: globalThis.KeyboardEvent) => {
      if (
        !event.defaultPrevented &&
        !event.altKey &&
        event.shiftKey &&
        (event.metaKey || event.ctrlKey) &&
        event.key.toLowerCase() === 'a'
      ) {
        event.preventDefault()
        returnToConversation()
      }
    }
    window.addEventListener('keydown', focusConversation)
    return () => window.removeEventListener('keydown', focusConversation)
  }, [returnToConversation])

  const selectScene = (next: Scene, focus = false) => {
    setTaskRequest(null)
    setScene(next)
    if (focus) tabs.current[SCENES.indexOf(next)]?.focus()
  }
  const navigateTabs = (
    event: KeyboardEvent<HTMLButtonElement>,
    index: number
  ) => {
    const next =
      event.key === 'ArrowRight'
        ? (index + 1) % SCENES.length
        : event.key === 'ArrowLeft'
          ? (index + SCENES.length - 1) % SCENES.length
          : event.key === 'Home'
            ? 0
            : event.key === 'End'
              ? SCENES.length - 1
              : null
    if (next === null) return
    event.preventDefault()
    selectScene(SCENES[next], true)
  }

  return (
    <div
      className='l0-welcome'
      data-testid='l0-conversation'
      data-scene={scene}
      onKeyDown={(event) => {
        if (
          event.key === 'Escape' &&
          !event.defaultPrevented &&
          scene !== 'chat'
        ) {
          event.preventDefault()
          selectScene('chat', true)
        }
      }}
    >
      <header className='l0-topbar'>
        <span className='l0-wordmark' aria-hidden='true'>
          LMM<span>/</span>
        </span>
        <div className='l0-topbar-actions'>
          <button
            type='button'
            className='l0-account-status'
            data-status={status}
            aria-controls='l0-panel-access'
            onClick={() => selectScene('access', true)}
          >
            <span className='l0-status-dot' aria-hidden='true' />
            <span aria-live='polite'>{statusLabel}</span>
            <Arrow diagonal />
          </button>
          <details
            ref={helpMenu}
            className='l0-help-menu'
            onKeyDown={closeDisclosure}
          >
            <summary aria-label={copy.help}>?</summary>
            <div className='l0-help-content'>
              <button
                type='button'
                onClick={() => requestAssistantOpen('human')}
              >
                {copy.support}
                <Arrow diagonal />
              </button>
              <button
                type='button'
                onClick={() => requestAssistantOpen('plan')}
              >
                {copy.plans}
                <Arrow diagonal />
              </button>
              <p>{copy.privacyNote}</p>
            </div>
          </details>
        </div>
      </header>

      {taskRequest ? (
        <section className='l0-assistant-task' data-testid='l0-assistant-task'>
          <div className='l0-task-navigation'>
            <Button variant='ghost' onClick={returnToConversation}>
              <span aria-hidden='true'>←</span>
              {t('Back to conversation')}
            </Button>
          </div>
          <Suspense fallback={<p role='status'>{t('Loading...')}</p>}>
            <AssistantTaskPanel
              mode='page'
              open
              initialPreset={taskRequest.preset}
              initialMessage={taskRequest.message}
              initialMessageRevision={taskRevision}
              autoSendRequestId={
                taskRequest.autoSend && consumedRequestId !== taskRequest.id
                  ? taskRequest.id
                  : undefined
              }
              onAutoSendConsumed={(id) => {
                consumeQueuedAssistantRequest(id)
                setConsumedRequestId(id)
              }}
              onOpenChange={(open) => {
                if (!open) returnToConversation()
              }}
              onConversationReset={() =>
                setTaskRequest((current) =>
                  current
                    ? {
                        ...current,
                        preset: undefined,
                        message: undefined,
                        autoSend: false,
                      }
                    : current
                )
              }
            />
          </Suspense>
        </section>
      ) : null}

      <div className='l0-stage' hidden={taskRequest !== null}>
        <div
          className='l0-cloud'
          ref={cloudRef}
          data-testid='l0-token-cloud'
          data-cloud-scene={scene}
        >
          <svg
            className='l0-cloud-fallback'
            viewBox='0 0 720 320'
            aria-hidden='true'
          >
            {FALLBACK_TOKENS.map((token, index) => {
              const p = projectL0Token(token, 0)
              return (
                <circle
                  key={index}
                  cx={360 + p.x * 200}
                  cy={160 + p.y * 200}
                  r={0.8 + p.depth}
                  opacity={0.15 + p.depth * 0.6}
                />
              )
            })}
          </svg>
          <canvas aria-hidden='true' />
          <button
            type='button'
            className='l0-cloud-toggle'
            data-cloud-pause
            aria-pressed='false'
            aria-label={copy.toggleMotion}
          >
            <svg viewBox='0 0 24 24' fill='none' aria-hidden='true'>
              <path className='l0-pause-icon' d='M9 7v10M15 7v10' />
              <path className='l0-play-icon' d='m9 6 9 6-9 6z' />
            </svg>
          </button>
        </div>

        <div
          className='l0-scene-tabs'
          role='tablist'
          aria-label={copy.navigation}
        >
          <span
            className='l0-tab-indicator'
            aria-hidden='true'
            style={{ transform: `translateX(${SCENES.indexOf(scene) * 100}%)` }}
          />
          {SCENES.map((item, index) => (
            <button
              key={item}
              ref={(node) => {
                tabs.current[index] = node
              }}
              id={`l0-tab-${item}`}
              role='tab'
              type='button'
              aria-selected={scene === item}
              aria-controls={`l0-panel-${item}`}
              tabIndex={scene === item ? 0 : -1}
              onClick={() => selectScene(item)}
              onKeyDown={(event) => navigateTabs(event, index)}
            >
              <SceneIcon scene={item} />
              <span>{labels[item]}</span>
            </button>
          ))}
        </div>

        <div className='l0-panels'>
          <div
            id='l0-panel-chat'
            role='tabpanel'
            aria-labelledby='l0-tab-chat'
            hidden={scene !== 'chat'}
            className='l0-panel'
          >
            <L0CloudConversation
              cloudRef={cloudRef}
              active={scene === 'chat' && taskRequest === null}
            />
          </div>

          <div
            id='l0-panel-explore'
            role='tabpanel'
            aria-labelledby='l0-tab-explore'
            hidden={scene !== 'explore'}
            className='l0-panel'
          >
            <section
              className='l0-discover'
              aria-roledescription={copy.carousel}
              aria-label={copy.explore}
            >
              <div
                className='l0-discover-switch'
                role='group'
                aria-label={copy.explore}
              >
                {destinations.map((item, index) => (
                  <button
                    key={item.to}
                    type='button'
                    aria-pressed={index === discovery}
                    onClick={() => setDiscovery(index)}
                  >
                    {item.title}
                  </button>
                ))}
              </div>
              <div
                className='l0-discover-slide'
                key={selected.to}
                aria-live='polite'
                aria-atomic='true'
              >
                <div className='l0-discover-eyebrow' aria-hidden='true'>
                  <span>{selected.mark}</span>
                  <span>0{discovery + 1} / 03</span>
                </div>
                <h2>{selected.title}</h2>
                <p>{selected.note}</p>
                <Link to={selected.to} className='l0-discover-link'>
                  {selected.action}
                  <Arrow diagonal />
                </Link>
              </div>
              <div className='l0-discover-controls'>
                <div className='l0-discover-progress' aria-hidden='true'>
                  {destinations.map((item, index) => (
                    <i key={item.to} data-active={index === discovery} />
                  ))}
                </div>
                <button
                  type='button'
                  className='l0-previous'
                  aria-label={copy.previous}
                  onClick={() => setDiscovery((discovery + 2) % 3)}
                >
                  <Arrow />
                </button>
                <button
                  type='button'
                  aria-label={copy.next}
                  onClick={() => setDiscovery((discovery + 1) % 3)}
                >
                  <Arrow />
                </button>
              </div>
            </section>
          </div>

          <div
            id='l0-panel-access'
            role='tabpanel'
            aria-labelledby='l0-tab-access'
            hidden={scene !== 'access'}
            className='l0-panel'
          >
            <section
              className='l0-unlock'
              data-testid='l0-activation'
              data-access-mode={access.mode}
              aria-label={t('Account and access')}
            >
              <div className='l0-levels' aria-label='L0 → L1'>
                <span>L0</span>
                <i aria-hidden='true' />
                <span>L1</span>
              </div>
              <h2>
                {canTopUp
                  ? copy.unlockTitle
                  : access.mode === 'sync'
                    ? copy.sync
                    : copy.review}
              </h2>
              {canTopUp ? (
                <>
                  <p className='l0-policy-note'>{copy.description}</p>
                  <div className='l0-credit' data-testid='l0-paid-progress'>
                    {access.threshold > 0 ? (
                      <>
                        <span>{copy.remaining}</span>
                        <strong>{money(access.remaining)}</strong>
                      </>
                    ) : (
                      <span>{copy.minimum}</span>
                    )}
                  </div>
                  <button
                    type='button'
                    className='l0-primary'
                    data-testid='l0-topup-direct'
                    onClick={() => void navigate({ to: '/wallet' })}
                  >
                    {copy.topup}
                    <Arrow />
                  </button>
                  <details
                    className='l0-conditions'
                    onKeyDown={closeDisclosure}
                  >
                    <summary>
                      {copy.conditions}
                      <span aria-hidden='true'>+</span>
                    </summary>
                    <p>{copy.eligibility}</p>
                  </details>
                </>
              ) : (
                <>
                  <p className='l0-policy-note'>
                    {access.mode === 'sync'
                      ? copy.syncNote
                      : access.mode === 'review'
                        ? copy.reviewNote
                        : copy.unknown}
                  </p>
                  {access.mode === 'review' ? (
                    <button
                      type='button'
                      className='l0-primary'
                      onClick={() => requestAssistantOpen('onboarding')}
                    >
                      {copy.apply}
                      <Arrow />
                    </button>
                  ) : (
                    <button
                      type='button'
                      className='l0-primary'
                      disabled={!user || busy}
                      onClick={check}
                    >
                      {t('Reload account status')}
                      <Arrow />
                    </button>
                  )}
                </>
              )}
              <div className='l0-secondary'>
                {canTopUp && (
                  <button
                    type='button'
                    onClick={() => requestAssistantOpen('onboarding')}
                  >
                    {copy.apply}
                  </button>
                )}
                <button
                  type='button'
                  data-testid='l0-check-payment'
                  disabled={!user || busy}
                  onClick={check}
                >
                  {copy.check}
                </button>
              </div>
              <p className='l0-feedback' role='status' aria-live='polite'>
                {checkState ? copy[checkState] : null}
              </p>
            </section>
            <details
              className='l0-account'
              data-testid='l0-account-details'
              onKeyDown={closeDisclosure}
            >
              <summary>
                <span>{statusLabel}</span>
                <span className='l0-disclosure-mark' aria-hidden='true'>
                  +
                </span>
              </summary>
              <div className='l0-account-body'>
                {children}
                <details className='l0-oauth' onKeyDown={closeDisclosure}>
                  <summary>
                    {copy.connectPi}
                    <span className='l0-disclosure-mark' aria-hidden='true'>
                      +
                    </span>
                  </summary>
                  <PiOAuthGuide />
                </details>
                <SourceQuestionnaire />
              </div>
            </details>
          </div>
        </div>
      </div>
    </div>
  )
}
