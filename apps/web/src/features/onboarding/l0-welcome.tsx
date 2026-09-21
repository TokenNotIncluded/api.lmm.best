/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { ArrowRight01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { Link, useNavigate } from '@tanstack/react-router'
import { type FormEvent, type ReactNode, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Progress } from '@/components/ui/progress'
import { SourceQuestionnaire } from '@/features/acquisition/source-questionnaire'
import {
  requestAssistantOpen,
  requestAssistantSend,
} from '@/features/assistant/assistant-events'
import { getAssistantPromptValidation } from '@/features/assistant/assistant-prompt-validation'
import { PiOAuthGuide } from '@/features/guide/pi-oauth-guide'
import { toIntlLocale } from '@/i18n/languages'
import type { AuthUser } from '@/stores/auth-store'

import { getL0AccessCopy } from './l0-access-copy'
import { getL0PaidAccess } from './l0-paid-access'
import { mountL0TokenCloud } from './l0-token-cloud'
import { useL0AccessCheck } from './use-l0-access-check'

import './l0-welcome.css'

const FALLBACK_TOKENS = [
  '{',
  'const',
  '/',
  '[]',
  '=>',
  '}',
  'await',
  '+',
  '</>',
  'return',
  ';',
  '()',
  '*',
]

export function L0Welcome({
  user,
  children,
}: {
  user: AuthUser | null
  children: ReactNode
}) {
  const { t, i18n } = useTranslation()
  const navigate = useNavigate()
  const [prompt, setPrompt] = useState('')
  const cloudRef = useRef<HTMLDivElement>(null)
  const { state: checkState, check } = useL0AccessCheck(user?.id)
  const language = i18n.resolvedLanguage || i18n.language
  const copy = getL0AccessCopy(language)
  const access = getL0PaidAccess(user)
  const canTopUp = access.mode === 'topup'
  const showThreshold =
    (canTopUp || access.mode === 'sync') && access.threshold > 0
  const money = (value: number) =>
    new Intl.NumberFormat(toIntlLocale(language), {
      style: 'currency',
      currency: 'USD',
      maximumFractionDigits: 6,
    }).format(value)

  useEffect(() => {
    if (cloudRef.current) return mountL0TokenCloud(cloudRef.current)
  }, [])

  const validPrompt =
    prompt.trim().length > 0 &&
    !getAssistantPromptValidation(prompt, true).invalid
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (validPrompt) requestAssistantSend(undefined, prompt.trim())
  }

  return (
    <div className='l0-welcome' data-testid='l0-conversation'>
      <div className='l0-welcome-topline'>
        <span className='l0-welcome-wordmark' aria-hidden='true'>
          LMM /
        </span>
        <a
          href='#l0-activation-title'
          className='l0-welcome-account-link'
          aria-label={t('Account and access')}
        >
          <Badge variant='outline'>
            {t('L{{level}}', { level: user?.trust_level_info?.level ?? 0 })}
          </Badge>
          <span aria-hidden='true'>↗</span>
        </a>
      </div>

      <section className='l0-welcome-hero' aria-labelledby='l0-welcome-title'>
        <div className='l0-welcome-heading'>
          <h2 id='l0-welcome-title'>{copy.greeting}</h2>
          <div className='l0-cloud' ref={cloudRef} data-testid='l0-token-cloud'>
            <div className='l0-cloud-fallback' aria-hidden='true'>
              {FALLBACK_TOKENS.map((glyph) => (
                <span key={glyph}>{glyph}</span>
              ))}
            </div>
            <canvas aria-hidden='true' />
            <button type='button' data-cloud-pause aria-pressed='false'>
              <span className='l0-cloud-pause'>{t('Pause')}</span>
              <span className='l0-cloud-resume'>{t('Resume')}</span>
            </button>
          </div>
        </div>
        <div className='l0-welcome-composer'>
          <form onSubmit={submit}>
            <label htmlFor='l0-question' className='l0-welcome-prompt-label'>
              {t('What would you like to do?')}
            </label>
            <div className='l0-welcome-input-row'>
              <span className='l0-welcome-input-mark' aria-hidden='true'>
                ›
              </span>
              <Input
                id='l0-question'
                value={prompt}
                onChange={(event) => setPrompt(event.target.value)}
                maxLength={4000}
                placeholder={copy.prompt}
                aria-describedby='l0-privacy'
                className='l0-welcome-input'
              />
              <Button
                type='submit'
                disabled={!validPrompt}
                className='l0-welcome-send'
                aria-label={t('Ask AI assistant')}
              >
                <HugeiconsIcon icon={ArrowRight01Icon} aria-hidden='true' />
              </Button>
            </div>
          </form>
          <div className='l0-welcome-composer-meta'>
            <div className='l0-welcome-shortcuts'>
              <button
                type='button'
                onClick={() =>
                  requestAssistantSend(undefined, t('Help me choose a model'))
                }
              >
                {copy.models} <span aria-hidden='true'>↗</span>
              </button>
              <button
                type='button'
                onClick={() => requestAssistantOpen('client-setup')}
              >
                {copy.tools} <span aria-hidden='true'>↗</span>
              </button>
            </div>
            <p id='l0-privacy' className='l0-welcome-privacy'>
              {copy.privacy}
            </p>
          </div>
        </div>
      </section>

      <section
        className='l0-welcome-activation'
        data-testid='l0-activation'
        data-access-mode={access.mode}
        aria-labelledby='l0-activation-title'
      >
        <div className='l0-activation-main'>
          <div className='l0-activation-copy'>
            <div className='l0-activation-levels' aria-hidden='true'>
              <span>L0</span>
              <span className='l0-activation-connector'>→</span>
              <span>L1</span>
            </div>
            <div>
              <h2 id='l0-activation-title' tabIndex={-1}>
                {canTopUp
                  ? copy.title
                  : access.mode === 'sync'
                    ? copy.sync
                    : copy.review}
              </h2>
              <p>
                {canTopUp
                  ? copy.description
                  : access.mode === 'sync'
                    ? copy.syncNote
                    : access.mode === 'unknown'
                      ? copy.unknown
                      : copy.reviewNote}
              </p>
            </div>
          </div>
          <div className='l0-activation-actions'>
            {canTopUp ? (
              <Button
                type='button'
                className='l0-activation-primary'
                data-testid='l0-topup-direct'
                onClick={() => void navigate({ to: '/wallet' })}
              >
                {copy.topup}
                <HugeiconsIcon icon={ArrowRight01Icon} aria-hidden='true' />
              </Button>
            ) : (
              <Button
                type='button'
                variant='outline'
                className='l0-activation-primary'
                disabled={!user || checkState === 'checking'}
                onClick={check}
              >
                {t('Reload account status')}
              </Button>
            )}
            <a href='#l0-access-title'>
              {copy.apply} <span aria-hidden='true'>↗</span>
            </a>
          </div>
        </div>
        {showThreshold && (
          <div className='l0-activation-credit' data-testid='l0-paid-progress'>
            <span>
              {copy.remaining} <strong>{money(access.remaining)}</strong>
            </span>
            <Progress
              value={Math.min(100, (access.paid / access.threshold) * 100)}
              aria-label={t('Progress to L{{level}}', { level: 1 })}
            />
          </div>
        )}
        {canTopUp && access.threshold === 0 && (
          <p className='l0-activation-minimum'>{copy.minimum}</p>
        )}
        <div className='l0-activation-footer'>
          {canTopUp || access.mode === 'sync' ? (
            <details className='l0-activation-rules'>
              <summary>{copy.rules}</summary>
              <p>{copy.eligibility}</p>
            </details>
          ) : (
            <span />
          )}
          <button
            type='button'
            data-testid='l0-check-payment'
            disabled={!user || checkState === 'checking'}
            onClick={check}
          >
            {copy.check} <span aria-hidden='true'>↻</span>
          </button>
        </div>
        <p className='l0-activation-feedback' role='status' aria-live='polite'>
          {checkState ? copy[checkState] : null}
        </p>
      </section>

      <nav
        className='l0-welcome-destinations'
        aria-label={t('Explore before you commit')}
      >
        {[
          { to: '/pricing', title: t('Models and pricing'), mark: '[]' },
          { to: '/tool-market', title: t('Tool market'), mark: '/>' },
          { to: '/challenges', title: t('Browse open challenges'), mark: '{}' },
        ].map((item) => (
          <Link key={item.to} to={item.to}>
            <span className='l0-welcome-index' aria-hidden='true'>
              {item.mark}
            </span>
            <span>{item.title}</span>
            <span className='l0-welcome-destination-arrow' aria-hidden='true'>
              ↗
            </span>
          </Link>
        ))}
      </nav>

      <section className='l0-welcome-access' aria-labelledby='l0-access-title'>
        <div className='l0-welcome-access-intro'>
          <h2 id='l0-access-title' tabIndex={-1}>
            {copy.application}
          </h2>
          <button type='button' onClick={() => requestAssistantOpen('human')}>
            {copy.support} <span aria-hidden='true'>↗</span>
          </button>
        </div>
        <div className='l0-welcome-access-body'>{children}</div>
      </section>
      <div className='l0-welcome-bottom'>
        <details className='l0-welcome-oauth'>
          <summary>{copy.oauth}</summary>
          <PiOAuthGuide />
        </details>
        <button type='button' onClick={() => requestAssistantOpen('plan')}>
          {copy.plans} <span aria-hidden='true'>↗</span>
        </button>
      </div>
      <div className='l0-welcome-source'>
        <SourceQuestionnaire />
      </div>
    </div>
  )
}
