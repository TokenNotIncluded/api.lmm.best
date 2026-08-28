/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import {
  CancelCircleIcon,
  CheckmarkCircle02Icon,
  CircleIcon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'

import { getAssistantJourney, type AssistantJourneyStepId } from './api'

const journeyLabels: Record<AssistantJourneyStepId, string> = {
  ask_ai: 'Ask AI for help',
  get_recommendation: 'Get a recommendation',
  create_api_key: 'Create an API key',
  install_client: 'Install a client',
  configure_client: 'Configure the API key',
  first_api_call: 'Complete a real API call',
  earn_ai_gift: 'Chat with AI to earn a $0–$10 (Platform) new-user gift',
  accept_bounty: 'Accept an open-source bounty',
}

export function AssistantJourneyProgress(props: {
  presentation?: 'page' | 'popover'
}) {
  const { t } = useTranslation()
  const journeyQuery = useQuery({
    queryKey: ['assistant-journey'],
    queryFn: getAssistantJourney,
    staleTime: 15_000,
    refetchInterval: 30_000,
    refetchOnWindowFocus: true,
    retry: false,
  })
  const journey = journeyQuery.data
  if (!journey) return null

  const mainDone = journey.main.filter(
    (step) => step.status === 'completed'
  ).length
  const sideDone = journey.side.filter(
    (step) => step.status === 'completed'
  ).length
  const pagePresentation = props.presentation === 'page'

  return (
    <details
      className={cn(
        'group min-w-0',
        pagePresentation
          ? 'order-last w-full md:order-none md:w-auto md:relative'
          : 'relative'
      )}
      data-testid='assistant-journey'
    >
      <summary className='text-muted-foreground hover:text-foreground cursor-pointer list-none text-xs transition-colors md:whitespace-nowrap [&::-webkit-details-marker]:hidden'>
        {t('Main quest')} {mainDone}/{journey.main.length}
        <span className='px-1.5' aria-hidden='true'>
          ·
        </span>
        {t('Side quest')} {sideDone}/{journey.side.length}
      </summary>
      <div
        className={cn(
          'border-border/60 bg-background grid gap-5 border px-5 py-5',
          pagePresentation
            ? 'mt-3 w-full md:absolute md:top-8 md:right-0 md:z-30 md:mt-0 md:w-72 md:shadow-xl'
            : 'absolute top-8 right-0 z-30 w-72 shadow-xl'
        )}
        data-testid='assistant-journey-panel'
      >
        {[
          { title: t('Main quest'), steps: journey.main },
          { title: t('Side quest'), steps: journey.side },
        ].map((section) => (
          <section className='grid gap-2.5' key={section.title}>
            <h2 className='text-xs font-medium'>{section.title}</h2>
            <ol className='grid gap-2'>
              {section.steps.map((step) => {
                const complete = step.status === 'completed'
                const failed = step.status === 'failed'
                let icon = CircleIcon
                let iconClassName = 'size-3.5'
                if (complete) {
                  icon = CheckmarkCircle02Icon
                  iconClassName = 'text-success size-3.5'
                } else if (failed) {
                  icon = CancelCircleIcon
                  iconClassName = 'text-muted-foreground/60 size-3.5'
                }
                return (
                  <li
                    className='text-muted-foreground flex items-center gap-2 text-xs'
                    key={step.id}
                  >
                    <HugeiconsIcon
                      icon={icon}
                      className={iconClassName}
                      strokeWidth={2}
                      aria-hidden='true'
                    />
                    <span
                      className={
                        complete || failed ? 'line-through opacity-70' : ''
                      }
                    >
                      {t(journeyLabels[step.id])}
                    </span>
                  </li>
                )
              })}
            </ol>
          </section>
        ))}
      </div>
    </details>
  )
}
