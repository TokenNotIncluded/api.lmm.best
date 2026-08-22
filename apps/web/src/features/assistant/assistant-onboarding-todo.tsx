/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import {
  ArrowRight01Icon,
  CheckmarkCircle02Icon,
  ReloadIcon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'

import { getL1OnboardingTodo } from './api'
import { getAssistantOnboardingTodoSteps } from './assistant-onboarding-todo-state'

/** Resolve the next actionable step into a handler + label. */
function getNextStepAction(
  steps: ReturnType<typeof getAssistantOnboardingTodoSteps>,
  props: { onOpenKey: () => void; onOpenSetup: () => void },
  t: (key: string) => string
): { action: () => void; label: string } | null {
  const nextStep = steps.find((step) => !step.complete && step.available)
  if (!nextStep) return null
  if (nextStep.id === 'create_api_key') {
    return { action: props.onOpenKey, label: t('Create API key') }
  }
  if (nextStep.id === 'install_client' || nextStep.id === 'configure_client') {
    return { action: props.onOpenSetup, label: t('Open setup guide') }
  }
  return null
}

/**
 * First-use checklist, kept deliberately slim: one quiet row above the
 * conversation with the count, the next actionable step, and a refresh.
 * The full card body was removed — the assistant itself walks through the
 * steps, so the dialog does not need to restate them.
 */
export function AssistantOnboardingTodo(props: {
  userId: number
  enabled: boolean
  presentation?: 'default' | 'compact'
  onOpenKey: () => void
  onOpenSetup: () => void
}) {
  const { t } = useTranslation()
  const todoQuery = useQuery({
    queryKey: ['assistant-onboarding-todo', props.userId],
    queryFn: getL1OnboardingTodo,
    enabled: props.enabled,
    staleTime: 60_000,
    retry: false,
  })

  if (!props.enabled || todoQuery.isLoading) return null
  if (todoQuery.isError || !todoQuery.data?.eligibility.eligible) return null

  const steps = getAssistantOnboardingTodoSteps(todoQuery.data)
  const completedCount = steps.filter((step) => step.complete).length
  const isComplete = todoQuery.data.status === 'completed'
  const next = getNextStepAction(steps, props, t)

  return (
    <div
      className='border-border/60 bg-card/40 mx-3 mt-3 flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1.5 rounded-lg border px-3 py-2 sm:mx-4'
      data-testid='assistant-onboarding-todo'
      data-presentation={
        props.presentation === 'compact' ? 'compact' : 'default'
      }
    >
      <HugeiconsIcon
        icon={isComplete ? CheckmarkCircle02Icon : ReloadIcon}
        className={
          isComplete
            ? 'text-success size-4 shrink-0'
            : 'text-muted-foreground size-4 shrink-0'
        }
        strokeWidth={2}
        aria-hidden='true'
      />
      <span className='min-w-0 flex-1 truncate text-sm font-medium'>
        {t('First-use checklist')}
      </span>
      <Badge variant={isComplete ? 'secondary' : 'outline'}>
        {completedCount}/{steps.length}
      </Badge>
      <Button
        type='button'
        variant='ghost'
        size='icon-xs'
        aria-label={t('Refresh')}
        title={t('Refresh')}
        onClick={() => void todoQuery.refetch()}
      >
        <HugeiconsIcon icon={ReloadIcon} strokeWidth={2} aria-hidden='true' />
      </Button>
      {next && !isComplete ? (
        <Button
          type='button'
          variant='ghost'
          size='sm'
          className='h-7 px-2 text-xs'
          onClick={next.action}
        >
          {next.label}
          <HugeiconsIcon
            icon={ArrowRight01Icon}
            strokeWidth={2}
            data-icon='inline-end'
            aria-hidden='true'
          />
        </Button>
      ) : null}
      {isComplete ? (
        <span className='text-muted-foreground text-xs'>
          {t('Setup complete')}
        </span>
      ) : null}
    </div>
  )
}
