/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import {
  AiChat02Icon,
  ArrowRight01Icon,
  CheckmarkCircle02Icon,
  DashboardSquare01Icon,
  Key01Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { Link, useNavigate } from '@tanstack/react-router'
import { type FormEvent, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Progress } from '@/components/ui/progress'
import { Separator } from '@/components/ui/separator'
import { requestAssistantOpen } from '@/features/assistant/assistant-events'
import { ChallengeList } from '@/features/forge/challenge-list'
import { PiOAuthGuide } from '@/features/guide/pi-oauth-guide'
import {
  getAuthenticatedLandingRoute,
  getOnboardingState,
  isConsoleActivated,
} from '@/lib/console-activation'
import { useAuthStore } from '@/stores/auth-store'

import { AccessRequestDetails } from './access-request-details'
import { AccountStatus } from './account-status'
import { L0Welcome } from './l0-welcome'
import { useAccountNextStep } from './use-account-next-step'
import { useAuthUserRefresh } from './use-auth-user-refresh'

export function GettingStarted() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { refreshUser } = useAuthUserRefresh()
  const user = useAuthStore((state) => state.auth.user)
  const onboarding = getOnboardingState(user)
  const trustLevel = user?.trust_level_info?.level ?? 0
  const [prompt, setPrompt] = useState('')
  const { request } = useAccountNextStep()
  const accessRequest = request.data
  const requestLoaded = request.isSuccess
  const { refetch: refetchAccessRequest } = request

  useEffect(() => {
    if (onboarding.activationComplete) return
    const onFocus = () => {
      void refetchAccessRequest()
    }
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  }, [onboarding.activationComplete, refetchAccessRequest])

  useEffect(() => {
    if (!requestLoaded || accessRequest?.status !== 'approved') {
      return
    }

    void refreshUser().then(async (refreshedUser) => {
      if (refreshedUser?.developer_access_granted !== true) {
        return
      }
      await navigate({ to: getAuthenticatedLandingRoute(refreshedUser) })
    })
  }, [accessRequest?.status, navigate, refreshUser, requestLoaded])

  const submitPrompt = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const message = prompt.trim()
    if (!message) return
    requestAssistantOpen('onboarding', message)
  }

  const continueAfterApproval = async () => {
    const refreshedUser = await refreshUser()
    if (refreshedUser?.developer_access_granted !== true) return
    await navigate({ to: getAuthenticatedLandingRoute(refreshedUser) })
  }

  if (!onboarding.activationComplete) {
    return (
      <SectionPageLayout>
        <SectionPageLayout.Title>
          {t('Getting started')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button variant='ghost' size='sm' render={<Link to='/pricing' />}>
            <HugeiconsIcon
              icon={DashboardSquare01Icon}
              strokeWidth={2}
              data-icon='inline-start'
              aria-hidden='true'
            />
            {t('Models and pricing')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <L0Welcome user={user}>
            <AccessRequestDetails inline />
            {requestLoaded && accessRequest?.status === 'approved' && (
              <Button
                type='button'
                size='sm'
                className='mt-3 w-fit'
                onClick={() => void continueAfterApproval()}
              >
                {t('Continue setup')}
              </Button>
            )}
          </L0Welcome>
        </SectionPageLayout.Content>
      </SectionPageLayout>
    )
  }

  const stageLabel =
    onboarding.stage === 'complete' ? t('Setup complete') : t('Continue setup')

  const tutorialSteps = [
    {
      complete: onboarding.activationComplete,
      title: t('Request API access'),
      description: t(
        'Describe your intended API use to the AI assistant. Eligible applications may be approved automatically; others remain under review.'
      ),
      preset: 'onboarding' as const,
    },
    {
      complete: onboarding.credentialComplete,
      title: t('Create API key'),
      description: t('Create your first developer credential.'),
      preset: 'api-key' as const,
    },
    {
      complete: onboarding.firstRequestComplete,
      title: t('Send first request'),
      description: t('Send one request to complete setup.'),
      preset: 'client-setup' as const,
    },
  ]
  const completedTutorialSteps = tutorialSteps.filter(
    (step) => step.complete
  ).length
  const currentTutorialStep = tutorialSteps.findIndex((step) => !step.complete)
  const tutorialProgress = (completedTutorialSteps / tutorialSteps.length) * 100

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Getting started')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <AccountStatus />
        <div className='mx-auto flex w-full max-w-4xl flex-col gap-6 pb-10 sm:gap-8 sm:pb-14'>
          <section className='bg-muted/30 border-y px-5 py-8 sm:px-8 sm:py-12'>
            <div className='flex flex-col gap-5 sm:flex-row sm:items-start sm:justify-between'>
              <div className='max-w-2xl'>
                <p className='text-muted-foreground text-sm font-medium'>
                  {t('One conversation to get started')}
                </p>
                <h3 className='mt-2 text-2xl font-semibold sm:text-3xl'>
                  {t('Tell the AI assistant what you want to do')}
                </h3>
                <p className='text-muted-foreground mt-3 text-sm leading-6'>
                  {t(
                    'Ask for a setup guide, model ID, API key, usage report, plan comparison, or any other next step. The assistant can guide the action from here.'
                  )}
                </p>
              </div>
              <div className='flex shrink-0 flex-wrap gap-2'>
                <Badge variant='outline'>
                  {t('API access enabled')} ·{' '}
                  {t('L{{level}}', { level: trustLevel })}
                </Badge>
                <Badge variant='secondary'>{stageLabel}</Badge>
              </div>
            </div>

            <form
              className='mt-7 flex flex-col gap-3 sm:flex-row'
              onSubmit={submitPrompt}
            >
              <Input
                value={prompt}
                onChange={(event) => setPrompt(event.target.value)}
                maxLength={4000}
                className='h-12 flex-1'
                placeholder={t(
                  'For example: help me apply for API access and configure CC Switch'
                )}
                aria-label={t('Tell the AI assistant what you need')}
              />
              <Button type='submit' size='lg' disabled={!prompt.trim()}>
                <HugeiconsIcon
                  icon={AiChat02Icon}
                  strokeWidth={2}
                  data-icon='inline-start'
                  aria-hidden='true'
                />
                {t('Start with AI assistant')}
                <HugeiconsIcon
                  icon={ArrowRight01Icon}
                  strokeWidth={2}
                  data-icon='inline-end'
                  aria-hidden='true'
                />
              </Button>
            </form>
            <div
              className='mt-3 flex flex-wrap gap-2'
              aria-label={t(
                'Choose a common question or ask anything about using LMM.'
              )}
            >
              {[
                t('What can I do while access is under review?'),
                t('Which option is the best value?'),
                t('What are my Base URL, model ID, and API key?'),
                t('How do I set up Claude Code or CC Switch?'),
              ].map((question) => (
                <Button
                  key={question}
                  type='button'
                  variant='outline'
                  size='sm'
                  className='h-auto min-h-9 whitespace-normal'
                  onClick={() => requestAssistantOpen(undefined, question)}
                >
                  {question}
                </Button>
              ))}
            </div>
            <p className='text-muted-foreground mt-3 text-xs leading-5'>
              {t(
                'Never paste a password, API key, session cookie, or other secret into the conversation.'
              )}
            </p>
          </section>

          <PiOAuthGuide />

          <section
            className='border px-5 py-6 sm:px-8'
            aria-labelledby='getting-started-tutorial-title'
          >
            <div className='flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between'>
              <div>
                <h3
                  id='getting-started-tutorial-title'
                  className='text-sm font-semibold'
                >
                  {t('Three steps to get started')}
                </h3>
                <p className='text-muted-foreground mt-1 text-sm leading-6'>
                  {t(
                    'Complete these steps to finish the initial installation.'
                  )}
                </p>
              </div>
              <Badge
                variant={
                  onboarding.stage === 'complete' ? 'secondary' : 'outline'
                }
              >
                {completedTutorialSteps}/{tutorialSteps.length}
              </Badge>
            </div>

            <div className='mt-5 flex items-center gap-3'>
              <Progress
                value={tutorialProgress}
                aria-label={t('Three steps to get started')}
                className='flex-1'
              />
              <span className='text-muted-foreground shrink-0 text-xs tabular-nums'>
                {Math.round(tutorialProgress)}%
              </span>
            </div>

            <ol className='mt-6 grid gap-3 lg:grid-cols-3'>
              {tutorialSteps.map((step, index) => {
                const isCurrent = index === currentTutorialStep
                let markerClass =
                  'text-muted-foreground border-muted-foreground/40 flex size-7 shrink-0 items-center justify-center rounded-full border text-sm font-semibold'
                let statusLabel = t('Pending')
                if (isCurrent) {
                  markerClass =
                    'border-primary text-primary flex size-7 shrink-0 items-center justify-center rounded-full border text-sm font-semibold'
                  statusLabel = t('Current step')
                }
                if (step.complete) {
                  markerClass =
                    'bg-primary text-primary-foreground flex size-7 shrink-0 items-center justify-center rounded-full'
                  statusLabel = t('Completed')
                }
                return (
                  <li
                    key={step.title}
                    className={
                      isCurrent
                        ? 'border-primary/50 bg-primary/5 flex min-h-40 flex-col gap-4 border p-4'
                        : 'bg-muted/20 flex min-h-40 flex-col gap-4 border p-4'
                    }
                  >
                    <div className='flex items-start gap-3'>
                      <span className={markerClass} aria-hidden='true'>
                        {step.complete ? (
                          <HugeiconsIcon
                            icon={CheckmarkCircle02Icon}
                            className='size-4'
                            strokeWidth={2}
                          />
                        ) : (
                          index + 1
                        )}
                      </span>
                      <div className='min-w-0 flex-1'>
                        <p className='text-sm font-medium'>{step.title}</p>
                        <p className='text-muted-foreground mt-1 text-xs leading-5'>
                          {step.description}
                        </p>
                      </div>
                    </div>
                    <div className='mt-auto flex items-center justify-between gap-2'>
                      <Badge variant={step.complete ? 'secondary' : 'outline'}>
                        {statusLabel}
                      </Badge>
                      {isCurrent ? (
                        <Button
                          type='button'
                          variant='ghost'
                          size='sm'
                          onClick={() => requestAssistantOpen(step.preset)}
                        >
                          {t('Continue')}
                          <HugeiconsIcon
                            icon={ArrowRight01Icon}
                            strokeWidth={2}
                            data-icon='inline-end'
                            aria-hidden='true'
                          />
                        </Button>
                      ) : null}
                    </div>
                  </li>
                )
              })}
            </ol>
          </section>

          <section className='border px-5 py-5 sm:px-8'>
            <div className='flex items-start gap-3'>
              <span className='bg-primary text-primary-foreground flex size-9 shrink-0 items-center justify-center rounded-full'>
                <HugeiconsIcon
                  icon={AiChat02Icon}
                  className='size-5'
                  strokeWidth={2}
                  aria-hidden='true'
                />
              </span>
              <div className='min-w-0'>
                <h3 className='text-sm font-semibold'>
                  {t('What the assistant can do')}
                </h3>
                <p className='text-muted-foreground mt-1 text-sm leading-6'>
                  {t(
                    'It can compare packages and discounts, calculate cost, show model IDs and Base URL, create a key after confirmation, teach Claude/Codex/CC Switch setup, analyze historical calls, explain invitations and bounties, or forward a free request to an administrator.'
                  )}
                </p>
              </div>
            </div>
          </section>

          <section className='border px-5 py-6 sm:px-8'>
            <div className='flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between'>
              <div>
                <h3 className='text-sm font-semibold'>
                  {t('Continue with your first integration')}
                </h3>
                <p className='text-muted-foreground mt-1 text-sm leading-6'>
                  {t(
                    'Ask the assistant to create a key, configure a client, or send a first test request.'
                  )}
                </p>
              </div>
              <Button
                variant='outline'
                onClick={() => requestAssistantOpen('client-setup')}
              >
                <HugeiconsIcon
                  icon={Key01Icon}
                  strokeWidth={2}
                  data-icon='inline-start'
                  aria-hidden='true'
                />
                {t('Open setup guide')}
              </Button>
            </div>
          </section>

          <section className='border-y px-5 py-5 sm:px-8'>
            <div className='flex flex-wrap items-center justify-between gap-3'>
              <div>
                <h3 className='text-sm font-semibold'>{t('Quick links')}</h3>
                <p className='text-muted-foreground mt-1 text-sm'>
                  {t('You can return to this guided conversation at any time.')}
                </p>
              </div>
              <div className='flex flex-wrap gap-2'>
                {/*
                  L0 accounts are confined to the contributor surface, so the
                  console links would only bounce them back to this page.
                */}
                {isConsoleActivated(user) ? (
                  <>
                    <Button variant='outline' render={<Link to='/dashboard' />}>
                      <HugeiconsIcon
                        icon={DashboardSquare01Icon}
                        strokeWidth={2}
                        data-icon='inline-start'
                        aria-hidden='true'
                      />
                      {t('Dashboard')}
                    </Button>
                    <Button
                      variant='outline'
                      render={<Link to='/open-source-bounties' />}
                    >
                      {t('Open-source bounties')}
                    </Button>
                  </>
                ) : (
                  <>
                    <Button
                      variant='outline'
                      render={<Link to='/tool-market' />}
                    >
                      <HugeiconsIcon
                        icon={DashboardSquare01Icon}
                        strokeWidth={2}
                        data-icon='inline-start'
                        aria-hidden='true'
                      />
                      {t('Tool market')}
                    </Button>
                    <Button
                      variant='outline'
                      render={<Link to='/challenges' />}
                    >
                      {t('Browse challenges')}
                    </Button>
                  </>
                )}
              </div>
            </div>
          </section>

          <Separator />
          <ChallengeList
            limit={3}
            hideWhenUnavailable
            heading={t('Optional open-source challenges')}
            description={t(
              'Contributions can earn account credit, but they do not activate access.'
            )}
          />
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
