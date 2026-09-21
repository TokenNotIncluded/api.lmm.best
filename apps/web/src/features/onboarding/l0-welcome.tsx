/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import {
  ArrowRight01Icon,
  CheckmarkCircle02Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { Link } from '@tanstack/react-router'
import { type FormEvent, type ReactNode, useState } from 'react'
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

export function L0Welcome({
  user,
  children,
}: {
  user: AuthUser | null
  children: ReactNode
}) {
  const { t, i18n } = useTranslation()
  const [prompt, setPrompt] = useState('')
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
    <div
      className='mx-auto w-full max-w-6xl pb-12 sm:pb-16'
      data-testid='l0-conversation'
    >
      <div className='grid border-b xl:grid-cols-[minmax(0,1.45fr)_minmax(19rem,1fr)]'>
        <section
          className='min-w-0 py-8 sm:py-12 xl:pr-10'
          aria-labelledby='l0-welcome-title'
        >
          <h2
            id='l0-welcome-title'
            className='max-w-xl text-3xl leading-tight font-semibold tracking-tight text-balance sm:text-4xl'
          >
            {t('Your next idea starts here.')}
          </h2>
          <p className='text-muted-foreground mt-5 max-w-lg text-base leading-7'>
            {t(
              'Find a model, plan your first integration, or explore tools. Start with what you want to make.'
            )}
          </p>
          <form onSubmit={submit} className='mt-8 space-y-3'>
            <label htmlFor='l0-question' className='text-sm font-medium'>
              {t('What would you like to do?')}
            </label>
            <div className='bg-card focus-within:border-ring flex flex-col gap-3 rounded-xl border p-3 sm:flex-row sm:items-center'>
              <Input
                id='l0-question'
                value={prompt}
                onChange={(event) => setPrompt(event.target.value)}
                maxLength={4000}
                placeholder={t('Describe your idea or ask a question')}
                aria-describedby='l0-privacy'
                className='h-11 min-w-0 flex-1 border-0 bg-transparent text-base shadow-none focus-visible:ring-0 dark:bg-transparent'
              />
              <Button
                type='submit'
                disabled={!validPrompt}
                className='h-11 px-4'
              >
                {t('Ask AI assistant')}
                <HugeiconsIcon
                  icon={ArrowRight01Icon}
                  aria-hidden='true'
                  data-icon='inline-end'
                />
              </Button>
            </div>
          </form>
          <div className='mt-4 flex flex-wrap gap-2'>
            <Button
              variant='outline'
              className='h-auto min-h-11 px-3 py-2 whitespace-normal'
              onClick={() =>
                requestAssistantSend(undefined, t('Help me choose a model'))
              }
            >
              {t('Help me choose a model')}
            </Button>
            <Button
              variant='outline'
              className='h-auto min-h-11 px-3 py-2 whitespace-normal'
              onClick={() => requestAssistantOpen('client-setup')}
            >
              {t('Connect my coding tools')}
            </Button>
          </div>
          <p
            id='l0-privacy'
            className='text-muted-foreground mt-5 max-w-lg text-xs leading-5'
          >
            {t(
              'Never paste a password, API key, session cookie, or other secret into the conversation.'
            )}
          </p>
          <div className='mt-8 flex flex-wrap items-center gap-x-5 gap-y-2 border-t pt-5'>
            <span className='text-muted-foreground text-sm'>
              {t('Need a person?')}
            </span>
            <Button
              variant='link'
              className='h-auto min-h-11 px-0'
              onClick={() => requestAssistantOpen('human')}
            >
              {t('Talk to support')}
            </Button>
          </div>
        </section>

        <aside
          className='bg-muted/40 min-w-0 rounded-xl p-5 sm:p-7 xl:my-8'
          aria-labelledby='l0-access-title'
        >
          <div className='flex flex-wrap items-center justify-between gap-3'>
            <h2 id='l0-access-title' className='text-xl font-semibold'>
              {t('Make it yours with L1')}
            </h2>
            <Badge variant='outline'>
              {t('L{{level}}', { level: user?.trust_level_info?.level ?? 0 })}
            </Badge>
          </div>
          <p className='text-muted-foreground mt-3 text-sm leading-6'>
            {t('API keys and developer tools unlock after access approval.')}
          </p>
          <ul className='my-5 space-y-3 text-sm'>
            {[
              t('Developer console access'),
              t('Your own API keys'),
              t('Usage and spending history'),
            ].map((benefit) => (
              <li key={benefit} className='flex items-start gap-2'>
                <HugeiconsIcon
                  icon={CheckmarkCircle02Icon}
                  className='text-primary mt-0.5 size-4 shrink-0'
                  aria-hidden='true'
                />
                {benefit}
              </li>
            ))}
          </ul>
          {showThreshold ? (
            <div
              className='space-y-3 border-t pt-5'
              data-testid='l0-paid-progress'
            >
              <p className='text-sm font-medium'>
                {t('{{amount}} eligible credit to unlock L1', {
                  amount: money(threshold),
                })}
              </p>
              <Progress
                value={Math.min(100, (paidAmount / threshold) * 100)}
                aria-label={t('Progress to L{{level}}', { level: 1 })}
              />
              <p className='text-muted-foreground text-xs leading-5'>
                {t(
                  '{{amount}} remaining. Only eligible external top-ups count; LinuxDO Credit is excluded.',
                  { amount: money(remaining) }
                )}
              </p>
            </div>
          ) : null}
          <Button
            className='mt-5 h-auto min-h-11 w-full px-4 py-2 whitespace-normal'
            onClick={() => requestAssistantOpen('plan')}
          >
            {t('Explore plans and top-ups')}
            <HugeiconsIcon
              icon={ArrowRight01Icon}
              data-icon='inline-end'
              aria-hidden='true'
            />
          </Button>
          <p className='text-muted-foreground mt-3 text-xs leading-5'>
            {t(
              'Review the price and payment options with the assistant before you pay.'
            )}
          </p>
          <div className='mt-6 border-t pt-5'>
            <h3 className='mb-3 text-sm font-semibold'>
              {t('Apply with your use case')}
            </h3>
            <p className='text-muted-foreground mb-4 text-xs leading-5'>
              {t(
                'Describe what you want to build. Eligible requests may be approved automatically; others go to review.'
              )}
            </p>
            {children}
          </div>
        </aside>
      </div>

      <section className='py-8 sm:py-10' aria-labelledby='l0-explore-title'>
        <h2 id='l0-explore-title' className='text-lg font-semibold'>
          {t('Explore before you commit')}
        </h2>
        <div className='mt-5 divide-y border-y'>
          {[
            {
              to: '/pricing',
              title: t('Models and pricing'),
              description: t(
                'Compare models and prices before your first request.'
              ),
            },
            {
              to: '/tool-market',
              title: t('Tool market'),
              description: t(
                'Explore available tools and their access requirements.'
              ),
            },
            {
              to: '/challenges',
              title: t('Browse open challenges'),
              description: t(
                'Find a project to contribute to. Contributions do not automatically unlock API access.'
              ),
            },
          ].map((item) => (
            <Link
              key={item.to}
              to={item.to}
              className='group hover:bg-muted/40 focus-visible:ring-ring grid min-h-20 items-center gap-2 rounded-sm px-3 py-5 outline-none focus-visible:ring-2 sm:grid-cols-[12rem_1fr_auto] sm:gap-6'
            >
              <span className='flex items-center justify-between gap-3 font-medium'>
                {item.title}
                <HugeiconsIcon
                  icon={ArrowRight01Icon}
                  className='size-4 sm:hidden'
                  aria-hidden='true'
                />
              </span>
              <span className='text-muted-foreground text-sm leading-6'>
                {item.description}
              </span>
              <HugeiconsIcon
                icon={ArrowRight01Icon}
                className='text-muted-foreground hidden size-5 sm:block'
                aria-hidden='true'
              />
            </Link>
          ))}
        </div>
      </section>
      <details className='group border-b pb-5'>
        <summary className='focus-visible:ring-ring cursor-pointer rounded-sm py-3 text-sm font-medium outline-none focus-visible:ring-2'>
          {t('Already use Pi? Connect with OAuth')}
        </summary>
        <PiOAuthGuide />
      </details>
      <div className='mt-6'>
        <SourceQuestionnaire />
      </div>
    </div>
  )
}
