/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { ArrowRight01Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { Link } from '@tanstack/react-router'
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

import { mountL0TokenCloud } from './l0-token-cloud'

import './l0-welcome.css'

export function L0Welcome({
  user,
  children,
}: {
  user: AuthUser | null
  children: ReactNode
}) {
  const { t, i18n } = useTranslation()
  const [prompt, setPrompt] = useState('')
  const cloudRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (cloudRef.current) return mountL0TokenCloud(cloudRef.current)
  }, [])
  const threshold = user?.onboarding?.paid_activation_min_amount
  const paidEnabled = user?.onboarding?.paid_activation_enabled === true
  const showThreshold =
    paidEnabled &&
    !user?.trust_level_info?.overridden &&
    typeof threshold === 'number' &&
    Number.isFinite(threshold) &&
    threshold > 0
  const paidAmount = Math.max(0, user?.trust_level_info?.paid_amount ?? 0)
  const remaining = showThreshold ? Math.max(0, threshold - paidAmount) : 0
  const money = (value: number) =>
    new Intl.NumberFormat(
      toIntlLocale(i18n.resolvedLanguage || i18n.language),
      {
        style: 'currency',
        currency: 'USD',
        maximumFractionDigits: 6,
      }
    ).format(value)
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
        <span className='l0-welcome-wordmark' aria-hidden='true'>LMM /</span>
        <a href='#l0-access-title' className='l0-welcome-account-link'>
          {t('Account and access')}
          <Badge variant='outline'>
            {t('L{{level}}', { level: user?.trust_level_info?.level ?? 0 })}
          </Badge>
          <span aria-hidden='true'>↗</span>
        </a>
      </div>

      <section className='l0-welcome-hero' aria-labelledby='l0-welcome-title'>
        <div className='l0-cloud' ref={cloudRef} data-testid='l0-token-cloud'>
          <div className='l0-cloud-fallback' aria-hidden='true'>
            {['{', 'const', '/', '[]', '=>', '}', 'await', '+', '</>', 'return', ';', '()', '*'].map((glyph, index) => (
              <span key={index}>{glyph}</span>
            ))}
          </div>
          <canvas aria-hidden='true' />
          <button type='button' data-cloud-pause aria-pressed='false'>
            <span className='l0-cloud-pause'>{t('Pause')}</span>
            <span className='l0-cloud-resume'>{t('Resume')}</span>
          </button>
        </div>
        <div className='l0-welcome-composer'>
          <h2 id='l0-welcome-title'>{t('Your next idea starts here.')}</h2>
          <form onSubmit={submit}>
            <label htmlFor='l0-question' className='l0-welcome-prompt-label'>
              {t('What would you like to do?')}
            </label>
            <div className='l0-welcome-input-row'>
              <Input
                id='l0-question'
                value={prompt}
                onChange={(event) => setPrompt(event.target.value)}
                maxLength={4000}
                placeholder={t('Describe your idea or ask a question')}
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
          <div className='l0-welcome-shortcuts'>
            <button
              type='button'
              onClick={() => requestAssistantSend(undefined, t('Help me choose a model'))}
            >
              {t('Help me choose a model')} <span aria-hidden='true'>↗</span>
            </button>
            <button type='button' onClick={() => requestAssistantOpen('client-setup')}>
              {t('Connect my coding tools')} <span aria-hidden='true'>↗</span>
            </button>
          </div>
          <p id='l0-privacy' className='l0-welcome-privacy'>
            {t('Never paste a password, API key, session cookie, or other secret into the conversation.')}
          </p>
        </div>
      </section>

      <nav className='l0-welcome-destinations' aria-label={t('Explore before you commit')}>
        {[
          { to: '/pricing', title: t('Models and pricing') },
          { to: '/tool-market', title: t('Tool market') },
          { to: '/challenges', title: t('Browse open challenges') },
        ].map((item, index) => (
          <Link key={item.to} to={item.to}>
            <span className='l0-welcome-index' aria-hidden='true'>0{index + 1}</span>
            <span>{item.title}</span>
            <span className='l0-welcome-destination-arrow' aria-hidden='true'>↗</span>
          </Link>
        ))}
      </nav>

      <section className='l0-welcome-access' aria-labelledby='l0-access-title'>
        <div className='l0-welcome-access-intro'>
          <h2 id='l0-access-title' tabIndex={-1}>{t('Make it yours with L1')}</h2>
          <p>{t('API keys and developer tools unlock after access approval.')}</p>
          <div className='l0-welcome-support'>
            <span>{t('Need a person?')}</span>
            <button type='button' onClick={() => requestAssistantOpen('human')}>
              {t('Talk to support')} <span aria-hidden='true'>↗</span>
            </button>
          </div>
        </div>
        <div className='l0-welcome-access-body'>
          <h3>{t('Apply with your use case')}</h3>
          <p className='l0-welcome-access-note'>
            {t('Describe what you want to build. Eligible requests may be approved automatically; others go to review.')}
          </p>
          {/* Keep every real request state visible; never replace children with demo status. */}
          {children}
          <div className='l0-welcome-plans'>
            {showThreshold ? (
              <div className='l0-welcome-paid' data-testid='l0-paid-progress'>
                <p>
                  {t('{{amount}} eligible credit to unlock L1', { amount: money(threshold) })}
                </p>
                <Progress
                  value={Math.min(100, (paidAmount / threshold) * 100)}
                  aria-label={t('Progress to L{{level}}', { level: 1 })}
                />
                <p className='l0-welcome-access-note'>
                  {t('{{amount}} remaining. Only eligible external top-ups count; LinuxDO Credit is excluded.', { amount: money(remaining) })}
                </p>
              </div>
            ) : null}
            <Button variant='outline' onClick={() => requestAssistantOpen('plan')}>
              {t('Explore plans and top-ups')}
              <HugeiconsIcon icon={ArrowRight01Icon} data-icon='inline-end' aria-hidden='true' />
            </Button>
            <p className='l0-welcome-access-note'>
              {t('Review the price and payment options with the assistant before you pay.')}
            </p>
          </div>
        </div>
      </section>
      <details className='l0-welcome-oauth'>
        <summary>{t('Already use Pi? Connect with OAuth')}</summary>
        <PiOAuthGuide />
      </details>
      <div className='l0-welcome-source'><SourceQuestionnaire /></div>
    </div>
  )
}
