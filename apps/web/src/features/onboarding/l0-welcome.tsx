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
  type ReactNode,
  useEffect,
  useRef,
} from 'react'
import { useTranslation } from 'react-i18next'

import { SourceQuestionnaire } from '@/features/acquisition/source-questionnaire'
import { requestAssistantOpen } from '@/features/assistant/assistant-events'
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

const FALLBACK_TOKENS = createL0Tokens(390).filter(
  (_, index) => index % 3 === 0
)

function Arrow({ diagonal = false }: { diagonal?: boolean }) {
  return (
    <svg viewBox='0 0 24 24' fill='none' aria-hidden='true'>
      <path
        d={diagonal ? 'M6 18 18 6M6 6h12v12' : 'M5 12h14m-5-5 5 5-5 5'}
      />
    </svg>
  )
}

export function L0Welcome({
  user,
  children,
}: {
  user: AuthUser | null
  children: ReactNode
}) {
  const { t, i18n } = useTranslation()
  const navigate = useNavigate()
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  const cloudRef = useRef<HTMLDivElement>(null)
  const { state: checkState, check } = useL0AccessCheck(user?.id)
  // Observe the parent's account-scoped query without adding requests or polling.
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
  const money = (value: number) =>
    new Intl.NumberFormat(toIntlLocale(language), {
      style: 'currency',
      currency: 'USD',
      maximumFractionDigits: 6,
    }).format(value)

  useEffect(() => {
    if (cloudRef.current) return mountL0TokenCloud(cloudRef.current)
  }, [])


  return (
    <div className='l0-welcome' data-testid='l0-conversation'>
      <section className='l0-stage' aria-labelledby='l0-welcome-title'>
        <div className='l0-cloud' ref={cloudRef} data-testid='l0-token-cloud'>
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
                  cx={360 + p.x * 145}
                  cy={160 + p.y * 145}
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
            aria-label={t('Pause')}
          >
            <svg viewBox='0 0 24 24' fill='none' aria-hidden='true'>
              <path className='l0-pause-icon' d='M9 7v10M15 7v10' />
              <path className='l0-play-icon' d='m9 6 9 6-9 6z' />
            </svg>
          </button>
        </div>

        <L0CloudConversation key={`${user?.id}:${sessionId}`} cloudRef={cloudRef} />

        <section
          className='l0-unlock'
          data-testid='l0-activation'
          data-access-mode={access.mode}
          aria-label={t('Account and access')}
        >
          <div className='l0-unlock-row'>
            <div className='l0-unlock-label'>
              <div className='l0-levels' aria-label='L0 → L1'>
                <span>L0</span>
                <i aria-hidden='true' />
                <span>L1</span>
              </div>
              <span className='l0-unlock-caption'>
                {canTopUp
                  ? copy.title
                  : access.mode === 'sync'
                    ? copy.sync
                    : copy.review}
              </span>
            </div>
            {canTopUp ? (
              <button
                type='button'
                className='l0-primary'
                data-testid='l0-topup-direct'
                onClick={() => void navigate({ to: '/wallet' })}
              >
                {copy.topup} <Arrow />
              </button>
            ) : access.mode === 'review' ? (
              <button
                type='button'
                className='l0-primary'
                onClick={() => requestAssistantOpen('onboarding')}
              >
                {copy.apply} <Arrow />
              </button>
            ) : (
              <button
                type='button'
                className='l0-primary'
                disabled={!user || busy}
                onClick={check}
              >
                {t('Reload account status')} <Arrow />
              </button>
            )}
          </div>
          {canTopUp && (
            <div className='l0-unlock-meta'>
              <span data-testid='l0-paid-progress'>
                {access.threshold > 0 ? (
                  <>
                    {copy.remaining} <b>{money(access.remaining)}</b>
                  </>
                ) : (
                  copy.minimum
                )}
              </span>
              <details
                className='l0-conditions'
                onKeyDown={(event) => {
                  if (event.key === 'Escape') {
                    event.currentTarget.open = false
                    event.currentTarget.querySelector('summary')?.focus()
                  }
                }}
              >
                <summary aria-label={copy.conditions}>ⓘ</summary>
                <div>
                  <p>{copy.description}</p>
                  <p>{copy.eligibility}</p>
                </div>
              </details>
            </div>
          )}
          {!canTopUp && (
            <p className='l0-policy-note'>
              {access.mode === 'sync'
                ? copy.syncNote
                : access.mode === 'review'
                  ? copy.reviewNote
                  : copy.unknown}
            </p>
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
      </section>

      <nav className='l0-dock' aria-label={t('Explore before you commit')}>
        <Link to='/pricing' aria-label={t('Models and pricing')}>
          {copy.models}
          <Arrow diagonal />
        </Link>
        <Link to='/tool-market' aria-label={t('Tool market')}>
          {copy.tools}
          <Arrow diagonal />
        </Link>
        <Link to='/challenges' aria-label={t('Browse open challenges')}>
          {copy.challenges}
          <Arrow diagonal />
        </Link>
        <button type='button' onClick={() => requestAssistantOpen('human')}>
          {copy.help}
          <Arrow diagonal />
        </button>
      </nav>
      <details className='l0-account' data-testid='l0-account-details'>
        <summary>
          <span aria-live='polite'>{statusLabel}</span>
          <span className='l0-disclosure-mark' aria-hidden='true'>+</span>
        </summary>
        <div className='l0-account-body'>
          {children}
          <p>
            {t('API keys and developer tools unlock after access approval.')}
          </p>
          <p>
            {t(
              'Never paste a password, API key, session cookie, or other secret into the conversation.'
            )}
          </p>
          <button type='button' onClick={() => requestAssistantOpen('plan')}>
            {t('Explore plans and top-ups')} <Arrow diagonal />
          </button>
          <details className='l0-oauth'>
            <summary>{t('Already use Pi? Connect with OAuth')}</summary>
            <PiOAuthGuide />
          </details>
          <SourceQuestionnaire />
        </div>
      </details>
    </div>
  )
}
