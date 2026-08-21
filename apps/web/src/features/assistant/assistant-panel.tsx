/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import {
  Alert02Icon,
  ArrowRight01Icon,
  CleanIcon,
  Maximize01Icon,
  Minimize01Icon,
  ReloadIcon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { PanelLeft, Plus, Square } from 'lucide-react'
import { nanoid } from 'nanoid'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  Conversation,
  ConversationContent,
  ConversationScrollButton,
} from '@/components/ai-elements/conversation'
import { Loader } from '@/components/ai-elements/loader'
import { Message, MessageContent } from '@/components/ai-elements/message'
import {
  PromptInput,
  PromptInputProvider,
  PromptInputBody,
  PromptInputFooter,
  PromptInputSubmit,
  PromptInputTextarea,
  usePromptInputController,
} from '@/components/ai-elements/prompt-input'
import { Response } from '@/components/ai-elements/response'
import {
  sideDrawerContentClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { LmmBrandMark } from '@/components/lmm-brand-mark'
import {
  Alert,
  AlertAction,
  AlertDescription,
  AlertTitle,
} from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Separator } from '@/components/ui/separator'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Skeleton } from '@/components/ui/skeleton'
import { isConsoleActivated } from '@/lib/console-activation'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import {
  getAssistantAvailableModels,
  getAssistantErrorInfo,
  getAssistantPreConversationPresets,
  getAssistantStatus,
  isAssistantRequestAborted,
  recordAssistantPreConversationPresetClick,
  sendAssistantMessage,
  type AssistantPreConversationPreset,
  type AssistantConversationHistoryItem,
  type AssistantConversationHistoryDetail,
  type AssistantChatMessage,
  type AssistantAccountDisableAction,
  type AssistantCreateKeyAction,
  type AssistantAdminChangeAction,
  type AssistantImageGenerationAction,
  type AssistantHumanSupportAction,
  type AssistantL1RecommendationAction,
  type AssistantNavigationAction,
  type AssistantToolTrace,
  type AssistantUserAction,
} from './api'
import {
  getAssistantAccountAccessState,
  type AssistantAccountAccessState,
} from './assistant-access'
import { AssistantAccountActionTool } from './assistant-account-action-tool'
import { AssistantActivationTool } from './assistant-activation-tool'
import { AssistantAdminChangeTool } from './assistant-admin-change-tool'
import { AssistantCostTool } from './assistant-cost-tool'
import {
  subscribeToAssistantOpen,
  type AssistantPresetId,
} from './assistant-events'
import { AssistantHandoffTool } from './assistant-handoff-tool'
import {
  AssistantHistory,
  AssistantHistoryConversation,
} from './assistant-history'
import { AssistantImageTool } from './assistant-image-tool'
import {
  getExplicitAssistantNavigation,
  getAssistantPresetForIntent,
  isExplicitAssistantL1Request,
} from './assistant-intent'
import { AssistantJourneyProgress } from './assistant-journey'
import { AssistantKeyTool } from './assistant-key-tool'
import {
  hasAssistantMessageSubstantialMeaning,
  redactAssistantMessageForDisplay,
  redactAssistantMessageForRequest,
} from './assistant-message-safety'
import { AssistantModelsTool } from './assistant-models-tool'
import { AssistantNewUserGift } from './assistant-new-user-gift'
import { AssistantOnboardingTodo } from './assistant-onboarding-todo'
import { AssistantPlanTool } from './assistant-plan-tool'
import { getAssistantPromptValidation } from './assistant-prompt-validation'
import { AssistantSetupTool } from './assistant-setup-tool'
import { AssistantToolCalls } from './assistant-tool-calls'
import { AssistantUsageTool } from './assistant-usage-tool'
import { AssistantUserActionTool } from './assistant-user-action-tool'
import { AssistantWeeklyDiscount } from './assistant-weekly-discount'

type AssistantActionPath =
  | '/'
  | '/getting-started'
  | '/pricing'
  | '/wallet'
  | '/usage-logs'
  | '/keys'
  | '/drawing'
  | '/profile'
  | '/open-source-bounties'
  | '/support'
  | '/users'

type AssistantAction =
  | { kind: 'route'; label: string; to: AssistantActionPath }
  | { kind: 'navigation'; label: string; href: string }
  | { kind: 'email'; label: string; href: string }
  | {
      kind: 'tool'
      label: string
      tool:
        | 'activation'
        | 'key'
        | 'cost'
        | 'handoff'
        | 'models'
        | 'plan'
        | 'setup'
        | 'usage'
    }

type ConversationEntry = {
  id: string
  role: 'user' | 'assistant'
  content: string
  tools?: AssistantToolTrace[]
  action?: AssistantAction
  adminChange?: AssistantAdminChangeAction
  imageAction?: AssistantImageGenerationAction
  error?: boolean
  notice?: boolean
  retry?: {
    message: string
    history: AssistantChatMessage[]
    presetId?: string
  }
}

function isAssistantKeyConfirmationMessage(message: string) {
  const normalized = message.trim().toLowerCase()
  return /^(是|好|可以|确认|确认创建|创建|没问题|yes|y|ok|okay|confirm|go ahead|proceed)[！!。.]?$/.test(
    normalized
  )
}

function assistantNavigationHref(action: AssistantNavigationAction): string {
  const entries = Object.entries(action.query)
  if (entries.length === 0) return action.path
  const query = new URLSearchParams()
  for (const [key, value] of entries) query.set(key, String(value))
  return `${action.path}?${query.toString()}`
}

function assistantNavigationLabel(
  action: AssistantNavigationAction,
  translate: (key: string) => string
): string {
  if (action.path === '/users' && action.query.filter) {
    return translate('Locate user')
  }
  if (action.path.startsWith('/usage-logs')) {
    return translate('Open usage logs')
  }
  if (action.path === '/profile') return translate('Open account bindings')
  return translate('Open page')
}

type AssistantPanelMode = 'mobile' | 'page' | 'rail'

function getBaseUrl(): string {
  if (typeof window === 'undefined') return 'https://api.lmm.best/v1'
  return `${window.location.origin}/v1`
}

const ASSISTANT_PRIVACY_NOTICE_COLLAPSE_DELAY_MS = 5_000
const ASSISTANT_LAYOUT_STORAGE_KEY = 'lmm-assistant-layout'

function readAssistantClassicLayout(): boolean {
  if (typeof window === 'undefined') return false
  try {
    return (
      window.localStorage.getItem(ASSISTANT_LAYOUT_STORAGE_KEY) === 'classic'
    )
  } catch {
    return false
  }
}

function AssistantClassicWelcome() {
  const { t } = useTranslation()
  const prompts = [
    [t('Examples'), t('Explain an API setup')],
    [t('Examples'), t('Compare live model pricing')],
    [t('Examples'), t('Draft an access request')],
  ]

  return (
    <div
      className='assistant-classic-welcome mx-auto flex w-full max-w-3xl flex-1 flex-col justify-center px-5 py-12 sm:px-8 sm:py-20'
      data-testid='assistant-classic-welcome'
    >
      <div className='flex items-start gap-3 sm:gap-4'>
        <div className='mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-md bg-[#19c37d] text-[#202123]'>
          <LmmBrandMark className='size-6' />
        </div>
        <div className='min-w-0'>
          <p className='text-xs font-medium tracking-wide text-[#b5b5bd] uppercase'>
            LMM Forge
          </p>
          <h2 className='mt-1 text-xl font-semibold tracking-tight text-[#f1f1f1] sm:text-2xl'>
            {t('How can I help?')}
          </h2>
          <p className='mt-2 max-w-xl text-sm leading-6 text-[#c5c5d2]'>
            {t('Guidance for plans, setup, API keys, costs, and support.')}
          </p>
        </div>
      </div>

      <div
        className='mt-10 border-y border-[#4b4d56]'
        aria-label={t('Examples')}
      >
        {prompts.map(([category, prompt], index) => (
          <div
            key={prompt}
            className='flex items-center gap-4 border-b border-[#4b4d56] py-4 last:border-b-0'
          >
            <span className='w-6 shrink-0 text-xs text-[#8e8ea0] tabular-nums'>
              {String(index + 1).padStart(2, '0')}
            </span>
            <span className='w-20 shrink-0 text-xs text-[#8e8ea0]'>
              {category}
            </span>
            <span className='min-w-0 text-sm leading-6 text-[#ececf1]'>
              {prompt}
            </span>
          </div>
        ))}
      </div>

      <p className='mt-5 text-xs leading-5 text-[#8e8ea0]'>
        {t('Permissions still apply')} · {t('Never share secrets in chat')} ·{' '}
        {t('Write actions need your confirmation')}
      </p>
    </div>
  )
}

function AssistantModernWelcome(props: {
  description: string
  restricted: boolean
}) {
  const { t } = useTranslation()
  const lanes = [
    [t('Live models and pricing'), t('Compare live model pricing')],
    [t('Step-by-step setup guidance'), t('Explain an API setup')],
    [t('Confirm sensitive actions yourself'), t('Draft an access request')],
  ]

  return (
    <div
      className='assistant-modern-welcome mx-auto flex w-full max-w-4xl flex-1 flex-col justify-center px-5 py-12 sm:px-10 sm:py-20'
      data-testid='assistant-modern-welcome'
    >
      <div className='assistant-modern-welcome__intro'>
        <div className='assistant-modern-welcome__mark' aria-label='LMM Forge'>
          <LmmBrandMark className='size-8' aria-hidden='true' />
        </div>
        <div className='max-w-2xl min-w-0'>
          <h2 className='text-3xl leading-[1.08] font-semibold tracking-tight text-balance sm:text-5xl'>
            {props.restricted
              ? t('What would you like to do?')
              : t('How can I help?')}
          </h2>
          <p className='text-muted-foreground mt-4 max-w-xl text-sm leading-7 sm:text-base'>
            {props.description}
          </p>
        </div>
      </div>

      <div
        className='assistant-modern-welcome__lanes'
        aria-label={t('Capabilities')}
      >
        {lanes.map(([label, prompt]) => (
          <div className='assistant-modern-welcome__lane' key={label}>
            <span className='text-sm font-medium'>{label}</span>
            <span className='text-muted-foreground text-sm'>{prompt}</span>
          </div>
        ))}
      </div>

      <p className='assistant-modern-welcome__notice text-muted-foreground text-xs leading-5'>
        {t('Permissions still apply')} · {t('Never share secrets in chat')} ·{' '}
        {t('Write actions need your confirmation')}
      </p>
    </div>
  )
}

function AssistantClassicSidebar(props: {
  onNewConversation: () => void
  onOpenHistory: () => void
  onToggleLayout: () => void
}) {
  const { t } = useTranslation()
  return (
    <aside
      className='hidden w-64 shrink-0 flex-col border-r border-[#4b4d56] bg-[#202123] text-[#ececf1] md:flex'
      data-testid='assistant-classic-sidebar'
    >
      <div className='flex items-center gap-3 px-4 py-5'>
        <LmmBrandMark className='size-8' />
        <div className='min-w-0'>
          <p className='truncate text-sm font-semibold'>LMM Forge</p>
          <p className='truncate text-xs text-[#b5b5bd]'>
            {t('Service guide')}
          </p>
        </div>
      </div>
      <div className='px-3'>
        <Button
          type='button'
          variant='outline'
          className='w-full justify-start border-[#565869] bg-transparent text-[#ececf1] hover:bg-[#2a2b32] hover:text-white'
          onClick={props.onNewConversation}
        >
          <Plus data-icon='inline-start' aria-hidden='true' />
          {t('New conversation')}
        </Button>
      </div>
      <nav
        className='mt-5 space-y-1 px-3'
        aria-label={t('Conversation history')}
      >
        <Button
          type='button'
          variant='ghost'
          className='w-full justify-start text-[#c5c5d2] hover:bg-[#2a2b32] hover:text-white'
          onClick={props.onOpenHistory}
        >
          <PanelLeft data-icon='inline-start' aria-hidden='true' />
          {t('Conversation history')}
        </Button>
      </nav>
      <div className='mt-auto border-t border-[#4b4d56] px-3 py-4'>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          className='w-full justify-start text-[#b5b5bd] hover:bg-[#2a2b32] hover:text-white'
          onClick={props.onToggleLayout}
        >
          <PanelLeft data-icon='inline-start' aria-hidden='true' />
          {t('Use modern layout')}
        </Button>
      </div>
    </aside>
  )
}

function AssistantActionButton(props: {
  action: AssistantAction
  onToolOpen: (
    tool:
      | 'activation'
      | 'key'
      | 'cost'
      | 'handoff'
      | 'models'
      | 'plan'
      | 'setup'
      | 'usage'
  ) => void
}) {
  const { action } = props
  if (action.kind === 'tool') {
    return (
      <Button variant='outline' onClick={() => props.onToolOpen(action.tool)}>
        {action.label}
        <HugeiconsIcon
          icon={ArrowRight01Icon}
          strokeWidth={2}
          data-icon='inline-end'
          aria-hidden='true'
        />
      </Button>
    )
  }
  if (action.kind === 'email') {
    return (
      <Button variant='outline' render={<a href={action.href} />}>
        {action.label}
        <HugeiconsIcon
          icon={ArrowRight01Icon}
          strokeWidth={2}
          data-icon='inline-end'
          aria-hidden='true'
        />
      </Button>
    )
  }

  if (action.kind === 'navigation') {
    return (
      <Button variant='outline' render={<a href={action.href} />}>
        {action.label}
        <HugeiconsIcon
          icon={ArrowRight01Icon}
          strokeWidth={2}
          data-icon='inline-end'
          aria-hidden='true'
        />
      </Button>
    )
  }

  return (
    <Button variant='outline' render={<Link to={action.to} />}>
      {action.label}
      <HugeiconsIcon
        icon={ArrowRight01Icon}
        strokeWidth={2}
        data-icon='inline-end'
        aria-hidden='true'
      />
    </Button>
  )
}

type AssistantTool = Exclude<
  Extract<AssistantAction, { kind: 'tool' }>['tool'],
  never
>

function getAssistantToolForTarget(
  target: AssistantPresetId | undefined
): AssistantTool | null {
  switch (target) {
    case 'onboarding':
      return 'activation'
    case 'api-key':
      return 'key'
    case 'client-setup':
      return 'setup'
    case 'cost':
      return 'cost'
    case 'human':
      return 'handoff'
    case 'models':
      return 'models'
    case 'plan':
      return 'plan'
    case 'usage':
      return 'usage'
    default:
      return null
  }
}

function getAssistantActionForTarget(
  target: AssistantPresetId | undefined,
  translate: (key: string) => string
): AssistantAction | undefined {
  switch (target) {
    case 'onboarding':
      return {
        kind: 'tool',
        label: translate('Unlock L1 access'),
        tool: 'activation',
      }
    case 'plan':
      return {
        kind: 'tool',
        label: translate('Compare live plans'),
        tool: 'plan',
      }
    case 'api-key':
      return {
        kind: 'tool',
        label: translate('View connection details'),
        tool: 'key',
      }
    case 'client-setup':
      return {
        kind: 'tool',
        label: translate('Open client setup guide'),
        tool: 'setup',
      }
    case 'bounty':
      return {
        kind: 'route',
        label: translate('Explore open-source bounties'),
        to: '/open-source-bounties',
      }
    case 'cost':
      return {
        kind: 'tool',
        label: translate('Calculate with live pricing'),
        tool: 'cost',
      }
    case 'usage':
      return {
        kind: 'tool',
        label: translate('Open usage statistics'),
        tool: 'usage',
      }
    case 'models':
      return {
        kind: 'tool',
        label: translate('View all currently available models'),
        tool: 'models',
      }
    case 'invitation':
      return {
        kind: 'route',
        label: translate('Open wallet and invitations'),
        to: '/wallet',
      }
    case 'human':
      return {
        kind: 'tool',
        label: translate('Send to an administrator'),
        tool: 'handoff',
      }
    default:
      return undefined
  }
}

function AssistantAccountStatusNotice(props: {
  state: Extract<AssistantAccountAccessState, 'loading' | 'error'>
  onRetry: () => void
}) {
  const { t } = useTranslation()
  if (props.state === 'loading') {
    return (
      <Card size='sm' aria-label={t('Loading...')}>
        <CardHeader>
          <CardTitle className='sr-only'>{t('Loading...')}</CardTitle>
          <Skeleton className='h-4 w-44' />
        </CardHeader>
        <CardContent>
          <Skeleton className='h-9 w-full' />
        </CardContent>
      </Card>
    )
  }

  return (
    <Alert variant='destructive'>
      <HugeiconsIcon icon={Alert02Icon} strokeWidth={2} aria-hidden='true' />
      <AlertTitle>{t('Unable to verify account access')}</AlertTitle>
      <AlertDescription>
        {t('Retry before using account-specific assistant tools.')}
      </AlertDescription>
      <AlertAction>
        <Button variant='outline' size='sm' onClick={props.onRetry}>
          {t('Retry')}
        </Button>
      </AlertAction>
    </Alert>
  )
}

function AssistantRouteStatusNotice(props: { onRetry: () => void }) {
  const { t } = useTranslation()
  return (
    <Alert variant='destructive' data-testid='assistant-route-unavailable'>
      <HugeiconsIcon icon={Alert02Icon} strokeWidth={2} aria-hidden='true' />
      <AlertTitle>{t('Assistant routing is unavailable')}</AlertTitle>
      <AlertDescription>
        {t(
          'The selected assistant group or model ID is unavailable. Ask an administrator to choose an enabled group and exact model ID, then retry.'
        )}
      </AlertDescription>
      <AlertAction>
        <Button variant='outline' size='sm' onClick={props.onRetry}>
          {t('Retry')}
        </Button>
      </AlertAction>
    </Alert>
  )
}

function assistantFailureMessage(
  error: unknown,
  translate: ReturnType<typeof useTranslation>['t']
) {
  const { code } = getAssistantErrorInfo(error)
  if (code === 'ASSISTANT_ROUTING_GROUP_UNAVAILABLE') {
    return translate(
      'Assistant routing is unavailable. Check the configured group and model ID, then retry.'
    )
  }
  if (code === 'ASSISTANT_MODEL_CATALOG_UNAVAILABLE') {
    return translate(
      'The assistant model catalog is temporarily unavailable. Check the model catalog and retry.'
    )
  }
  if (code === 'ASSISTANT_BUSY') {
    return translate('The assistant is busy right now. Please retry shortly.')
  }
  if (code) {
    return translate(
      'The AI assistant could not answer right now. Try again or contact support. (Error: {{code}}.)',
      { code }
    )
  }
  return translate(
    'The AI assistant could not answer right now. Try again or contact support.'
  )
}

function AssistantPromptInputSync(props: {
  initialMessage?: string
  initialMessageRevision?: number
}) {
  const {
    textInput: { setInput },
  } = usePromptInputController()
  const initialMessage = props.initialMessage?.trim() ?? ''
  const lastRequest = useRef<{
    message: string
    revision?: number
  } | null>(null)

  useEffect(() => {
    if (
      lastRequest.current?.message === initialMessage &&
      lastRequest.current?.revision === props.initialMessageRevision
    ) {
      return
    }

    lastRequest.current = {
      message: initialMessage,
      revision: props.initialMessageRevision,
    }
    if (initialMessage) setInput(initialMessage)
  }, [initialMessage, props.initialMessageRevision, setInput])

  return null
}

function AssistantPresetPrompts(props: {
  presets: AssistantPreConversationPreset[]
  onSelect: (preset: AssistantPreConversationPreset) => void
}) {
  const { t } = useTranslation()
  const {
    textInput: { setInput },
  } = usePromptInputController()
  if (props.presets.length === 0) return null

  return (
    <div
      className='mb-2 flex max-w-full flex-wrap gap-2 pb-1'
      role='group'
      aria-label={t('Choose a topic or write a message.')}
      data-testid='assistant-preset-prompts'
    >
      {props.presets.map((preset) => (
        <Button
          key={preset.id}
          type='button'
          variant='ghost'
          size='sm'
          className='bg-muted/40 h-auto min-h-8 shrink-0 rounded-full px-3 text-left whitespace-normal'
          onClick={() => {
            setInput(preset.prompt)
            props.onSelect(preset)
          }}
        >
          {preset.label || preset.prompt}
        </Button>
      ))}
    </div>
  )
}

function AssistantPromptComposer(props: {
  footerStatus: string
  placeholder: string
  privacyNoticeId: string
  classicLayout?: boolean
  restricted: boolean
  terminated: boolean
  routeUnavailable: boolean
  sending: boolean
  onStop: () => void
  onSubmit: (message: { text?: string }) => void | Promise<void>
}) {
  const { t } = useTranslation()
  const {
    textInput: { setInput, value },
  } = usePromptInputController()
  const onSubmit = props.onSubmit
  const validation = getAssistantPromptValidation(value, props.restricted)
  const hasText = value.trim().length > 0
  const showValidationError = hasText && validation.invalid
  const hintId = 'assistant-l0-input-hint'
  const describedBy = showValidationError
    ? `${props.privacyNoticeId} ${hintId}`
    : props.privacyNoticeId

  const handleSubmit = useCallback(
    (message: { text?: string }) => {
      const safeMessage = redactAssistantMessageForRequest(message.text ?? '')
      // PromptInput keeps provider state until the async submit resolves. Set
      // the safe value immediately so the raw textarea value cannot linger
      // while the assistant request is in flight.
      if (safeMessage.redacted) setInput(safeMessage.content)
      return onSubmit({ text: safeMessage.content })
    },
    [onSubmit, setInput]
  )

  return (
    <>
      <PromptInput
        onSubmit={handleSubmit}
        groupClassName={cn(
          'assistant-prompt-input has-[[data-slot=input-group-control]:focus-visible]:border-transparent has-[[data-slot=input-group-control]:focus-visible]:ring-0 rounded-xl border-transparent',
          props.classicLayout
            ? 'rounded-2xl border-[#565869] bg-[#40414f] text-[#ececf1] shadow-[0_8px_24px_rgba(0,0,0,0.22)] ring-1 ring-black/20'
            : 'assistant-modern-prompt border-border/70 bg-card/80 dark:bg-card/70'
        )}
        aria-label={t('Ask AI assistant')}
        data-testid='assistant-prompt-form'
      >
        <PromptInputBody>
          <PromptInputTextarea
            placeholder={
              props.terminated
                ? t('This conversation has ended. Start a new conversation.')
                : props.placeholder
            }
            aria-label={t('Ask AI assistant')}
            maxLength={4000}
            required={props.restricted}
            aria-describedby={describedBy}
            aria-invalid={hasText ? validation.invalid : undefined}
            disabled={
              props.sending || props.terminated || props.routeUnavailable
            }
            className={cn(
              'max-h-24 min-h-10 sm:max-h-32 sm:min-h-12',
              props.classicLayout && 'text-[#ececf1] placeholder:text-[#b5b5bd]'
            )}
          />
        </PromptInputBody>
        <PromptInputFooter
          className={cn(
            'px-2 py-1 pb-1.5',
            props.classicLayout && 'text-[#b5b5bd]'
          )}
        >
          <span className='min-w-0 flex-1 truncate text-xs'>
            {props.footerStatus}
          </span>
          {props.sending ? (
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={props.onStop}
              aria-label={t('Stop')}
            >
              <Square data-icon='inline-start' aria-hidden='true' />
              {t('Stop')}
            </Button>
          ) : (
            <PromptInputSubmit
              status='ready'
              disabled={
                props.terminated || props.routeUnavailable || validation.invalid
              }
              size='sm'
              className={
                props.classicLayout
                  ? 'bg-[#19c37d] text-[#202123] hover:bg-[#1aaf73]'
                  : undefined
              }
            >
              {t('Send')}
            </PromptInputSubmit>
          )}
        </PromptInputFooter>
      </PromptInput>
      {showValidationError ? (
        <p
          id={hintId}
          className={
            showValidationError
              ? 'text-destructive mt-2 px-1 text-xs leading-5'
              : 'text-muted-foreground mt-2 px-1 text-xs leading-5'
          }
          role={showValidationError ? 'alert' : 'status'}
          aria-live='polite'
        >
          {t('Please enter a message other than a single punctuation mark.')}
        </p>
      ) : null}
    </>
  )
}

function AssistantPanelHeader(props: {
  mode: AssistantPanelMode
  description: string
  classicLayout: boolean
  onNewConversation: () => void
  onToggleClassicLayout: () => void
  historyVisible: boolean
  historyDetail: boolean
  onOpenHistory: () => void
  onCloseHistory: () => void
  onToggleCollapsed?: () => void
  fullscreen?: boolean
  onToggleFullscreen?: () => void
}) {
  const { t } = useTranslation()

  if (props.classicLayout) {
    return (
      <header
        className={cn(
          'assistant-classic-header flex min-w-0 shrink-0 items-center gap-2 border-b border-[#4b4d56] bg-[#343541] px-3 py-3 text-[#ececf1] sm:gap-3 sm:px-5',
          props.mode === 'mobile' && 'pr-12'
        )}
        data-testid='assistant-classic-header'
      >
        <Button
          type='button'
          variant='ghost'
          size='icon-sm'
          className='shrink-0 text-[#c5c5d2] hover:bg-[#444654] hover:text-white md:hidden'
          aria-label={t('New conversation')}
          title={t('New conversation')}
          onClick={props.onNewConversation}
        >
          <Plus aria-hidden='true' />
        </Button>
        <LmmBrandMark className='size-7 sm:size-8' />
        <div className='min-w-0 flex-1'>
          <div className='flex min-w-0 items-center gap-2'>
            <h1 className='truncate text-sm font-semibold sm:text-base'>
              LMM Forge
            </h1>
            <span className='hidden rounded-full border border-[#565869] px-2 py-0.5 text-[10px] tracking-wide text-[#b5b5bd] uppercase sm:inline'>
              {t('Classic chat')}
            </span>
          </div>
          <p className='mt-0.5 hidden truncate text-xs text-[#b5b5bd] sm:block'>
            {props.description}
          </p>
        </div>
        <div className='flex min-w-0 shrink-0 items-center gap-0.5'>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            className='hidden text-[#c5c5d2] hover:bg-[#444654] hover:text-white sm:inline-flex'
            onClick={props.onNewConversation}
          >
            <Plus data-icon='inline-start' aria-hidden='true' />
            <span className='hidden lg:inline'>{t('New conversation')}</span>
          </Button>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            className='max-w-10 shrink-0 gap-1 px-2 text-[#c5c5d2] hover:bg-[#444654] hover:text-white sm:max-w-40'
            aria-label={
              props.historyVisible
                ? props.historyDetail
                  ? t('Conversation history')
                  : t('Back')
                : t('Conversation history')
            }
            title={
              props.historyVisible
                ? props.historyDetail
                  ? t('Conversation history')
                  : t('Back')
                : t('Conversation history')
            }
            onClick={
              props.historyVisible ? props.onCloseHistory : props.onOpenHistory
            }
          >
            <PanelLeft className='size-4 shrink-0' aria-hidden='true' />
            <span className='hidden truncate md:inline'>
              {props.historyVisible
                ? props.historyDetail
                  ? t('Conversation history')
                  : t('Back')
                : t('Conversation history')}
            </span>
          </Button>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            className='text-[#c5c5d2] hover:bg-[#444654] hover:text-white'
            aria-pressed={props.classicLayout}
            aria-label={t('Modern chat')}
            title={t('Modern chat')}
            data-testid='assistant-layout-toggle'
            onClick={props.onToggleClassicLayout}
          >
            <PanelLeft data-icon='inline-start' aria-hidden='true' />
            <span className='hidden sm:inline'>{t('Modern chat')}</span>
          </Button>
          {props.mode !== 'mobile' &&
          props.mode !== 'page' &&
          !props.fullscreen ? (
            <Button
              type='button'
              variant='ghost'
              size='icon-sm'
              className='text-[#c5c5d2] hover:bg-[#444654] hover:text-white'
              aria-label={t('Collapse')}
              title={t('Collapse')}
              data-testid='assistant-collapse'
              onClick={props.onToggleCollapsed}
            >
              <HugeiconsIcon
                icon={ArrowRight01Icon}
                strokeWidth={2}
                aria-hidden='true'
              />
            </Button>
          ) : null}
          {props.mode !== 'mobile' && props.mode !== 'page' ? (
            <Button
              type='button'
              variant='ghost'
              size='icon-sm'
              className='text-[#c5c5d2] hover:bg-[#444654] hover:text-white'
              aria-label={
                props.fullscreen
                  ? t('Exit full screen')
                  : t('Enter full screen')
              }
              title={
                props.fullscreen
                  ? t('Exit full screen')
                  : t('Enter full screen')
              }
              data-testid='assistant-fullscreen'
              onClick={props.onToggleFullscreen}
            >
              <HugeiconsIcon
                icon={props.fullscreen ? Minimize01Icon : Maximize01Icon}
                strokeWidth={2}
                aria-hidden='true'
              />
            </Button>
          ) : null}
        </div>
      </header>
    )
  }

  if (props.mode === 'page') {
    return (
      <header
        className={cn(
          'assistant-glass-surface assistant-modern-header border-border/70 flex min-w-0 shrink-0 flex-wrap items-center gap-3 border-b px-5 py-4 sm:px-8',
          props.historyVisible && 'bg-muted/10'
        )}
      >
        <div className='min-w-0 flex-1'>
          <h1 className='truncate text-sm font-semibold'>
            {t('Service guide')}
          </h1>
          <p className='text-muted-foreground mt-1 hidden truncate text-xs sm:block'>
            {props.description}
          </p>
        </div>
        <AssistantJourneyProgress presentation='page' />
        <div className='border-border flex items-center rounded-full border p-0.5'>
          <span className='bg-muted text-foreground rounded-full px-3 py-1 text-xs font-medium'>
            {t('Modern chat')}
          </span>
          <span className='text-muted-foreground px-1 text-xs'>/</span>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            className='h-7 rounded-full px-3 text-xs'
            aria-pressed={props.classicLayout}
            aria-label={t('Use classic layout')}
            data-testid='assistant-layout-toggle'
            onClick={props.onToggleClassicLayout}
          >
            {t('Classic chat')}
          </Button>
        </div>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          onClick={
            props.historyVisible ? props.onCloseHistory : props.onOpenHistory
          }
        >
          {props.historyVisible
            ? props.historyDetail
              ? t('Conversation history')
              : t('Back to conversation')
            : t('Conversation history')}
        </Button>
      </header>
    )
  }

  if (props.mode === 'mobile') {
    return (
      <SheetHeader
        className={sideDrawerHeaderClassName(
          'assistant-modern-header min-w-0 shrink-0 flex-row items-start justify-between gap-3 pr-12 pt-[max(0.75rem,env(safe-area-inset-top))]'
        )}
      >
        <div className='min-w-0 flex-1 text-left'>
          <SheetTitle className='truncate text-base'>
            {t('Service guide')}
          </SheetTitle>
          <SheetDescription className='mt-1 min-w-0 truncate text-xs'>
            {props.description}
          </SheetDescription>
        </div>
        <div className='flex shrink-0 items-center gap-1'>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            className='gap-1 px-2 text-xs'
            aria-label={t('Conversation history')}
            title={t('Conversation history')}
            onClick={
              props.historyVisible ? props.onCloseHistory : props.onOpenHistory
            }
          >
            <PanelLeft aria-hidden='true' />
            <span className='hidden min-[420px]:inline'>
              {props.historyVisible
                ? props.historyDetail
                  ? t('Conversation history')
                  : t('Back to conversation')
                : t('Conversation history')}
            </span>
          </Button>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            className='rounded-full px-2 text-xs'
            aria-pressed={props.classicLayout}
            data-testid='assistant-layout-toggle'
            onClick={props.onToggleClassicLayout}
          >
            {props.classicLayout ? t('Modern chat') : t('Classic chat')}
          </Button>
        </div>
      </SheetHeader>
    )
  }

  return (
    <header className='assistant-glass-surface assistant-modern-header border-border/70 flex min-w-0 shrink-0 items-start gap-2 border-b px-4 py-4 sm:gap-3 sm:px-5'>
      <div className='min-w-0 flex-1'>
        <h2 className='truncate text-base leading-6 font-semibold'>
          {t('Service guide')}
        </h2>
        <p className='text-muted-foreground mt-0.5 text-xs leading-5'>
          {props.description}
        </p>
      </div>
      <div className='flex min-w-0 shrink-0 items-center gap-0.5'>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          className='hidden max-w-28 truncate px-2 sm:inline-flex'
          aria-pressed={props.classicLayout}
          data-testid='assistant-layout-toggle'
          onClick={props.onToggleClassicLayout}
        >
          {props.classicLayout ? t('Modern chat') : t('Classic chat')}
        </Button>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          className='max-w-32 shrink-0 truncate px-2 sm:max-w-40'
          onClick={
            props.historyVisible ? props.onCloseHistory : props.onOpenHistory
          }
        >
          {props.historyVisible
            ? props.historyDetail
              ? t('Conversation history')
              : t('Back')
            : t('Conversation history')}
        </Button>
        {!props.fullscreen ? (
          <Button
            type='button'
            variant='ghost'
            size='icon-sm'
            aria-label={t('Collapse')}
            title={t('Collapse')}
            data-testid='assistant-collapse'
            onClick={props.onToggleCollapsed}
          >
            <HugeiconsIcon
              icon={ArrowRight01Icon}
              strokeWidth={2}
              aria-hidden='true'
            />
          </Button>
        ) : null}
        <Button
          type='button'
          variant='ghost'
          size='icon-sm'
          aria-label={
            props.fullscreen ? t('Exit full screen') : t('Enter full screen')
          }
          title={
            props.fullscreen ? t('Exit full screen') : t('Enter full screen')
          }
          data-testid='assistant-fullscreen'
          onClick={props.onToggleFullscreen}
        >
          <HugeiconsIcon
            icon={props.fullscreen ? Minimize01Icon : Maximize01Icon}
            strokeWidth={2}
            aria-hidden='true'
          />
        </Button>
      </div>
    </header>
  )
}

export function AssistantPanel(props: {
  open: boolean
  mode?: AssistantPanelMode
  collapsed?: boolean
  fullscreen?: boolean
  initialPreset?: AssistantPresetId
  initialMessage?: string
  initialMessageRevision?: number
  autoSendRequestId?: string
  onAutoSendConsumed?: (requestId: string) => void
  onOpenChange: (open: boolean) => void
  onConversationReset?: () => void
  onToggleCollapsed?: () => void
  onToggleFullscreen?: () => void
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const mode = props.mode ?? 'mobile'
  const onConversationReset = props.onConversationReset
  const panelVisible =
    mode === 'page'
      ? true
      : mode === 'rail'
        ? !props.collapsed || props.fullscreen === true
        : props.open
  const baseUrl = getBaseUrl()
  const [entries, setEntries] = useState<ConversationEntry[]>([])
  const [conversationId, setConversationId] = useState<number | null>(null)
  const [selectedPreConversationPresetId, setSelectedPreConversationPresetId] =
    useState<string | null>(null)
  const [conversationRestricted, setConversationRestricted] = useState(false)
  const [sending, setSending] = useState(false)
  const assistantAbortControllerRef = useRef<AbortController | null>(null)
  const [classicLayout, setClassicLayout] = useState(readAssistantClassicLayout)
  const submittedAutoSendIdRef = useRef<string | undefined>(undefined)
  const [recommendationDraft, setRecommendationDraft] =
    useState<AssistantL1RecommendationAction | null>(null)
  const [accountDisableDraft, setAccountDisableDraft] =
    useState<AssistantAccountDisableAction | null>(null)
  const [humanSupportAction, setHumanSupportAction] =
    useState<AssistantHumanSupportAction | null>(null)
  const [keyCreationAction, setKeyCreationAction] =
    useState<AssistantCreateKeyAction | null>(null)
  const [autoConfirmKeyToken, setAutoConfirmKeyToken] = useState<string | null>(
    null
  )
  const [userActionDraft, setUserActionDraft] =
    useState<AssistantUserAction | null>(null)
  const [historyView, setHistoryView] = useState<
    'list' | AssistantConversationHistoryItem | null
  >(null)
  const [activeTool, setActiveTool] = useState<
    | 'activation'
    | 'key'
    | 'cost'
    | 'handoff'
    | 'models'
    | 'plan'
    | 'setup'
    | 'usage'
    | null
  >(null)
  const openedTargetRef = useRef<AssistantPresetId | undefined>(undefined)
  const activeToolRegionRef = useRef<HTMLDivElement | null>(null)
  const [conversationResetRevision, setConversationResetRevision] = useState(0)
  const [privacyNoticeExpanded, setPrivacyNoticeExpanded] = useState(
    mode !== 'page'
  )
  const privacyNoticeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(
    null
  )
  useEffect(
    () => () => {
      assistantAbortControllerRef.current?.abort()
    },
    []
  )
  useEffect(() => {
    try {
      window.localStorage.setItem(
        ASSISTANT_LAYOUT_STORAGE_KEY,
        classicLayout ? 'classic' : 'modern'
      )
    } catch {
      // A private browsing context may deny storage; the in-memory toggle still works.
    }
  }, [classicLayout])
  const statusQuery = useQuery({
    queryKey: ['assistant-status'],
    queryFn: getAssistantStatus,
    enabled: panelVisible,
    staleTime: 30_000,
    retry: false,
  })
  const accountAccessState = getAssistantAccountAccessState(
    statusQuery.data,
    statusQuery.isError
  )
  const accountAccessConfirmed =
    accountAccessState === 'granted' || accountAccessState === 'restricted'
  const developerAccessGranted = accountAccessState === 'granted'
  const assistantRouteUnavailable = statusQuery.data?.route_available === false
  const preConversationPresetsQuery = useQuery({
    queryKey: ['assistant-pre-conversation-presets'],
    queryFn: getAssistantPreConversationPresets,
    // Presets are public onboarding guidance, not an L0-only capability.
    // Keep them available for L1/admin users too so returning users can still
    // discover the assistant's current workflows.
    enabled: panelVisible && accountAccessConfirmed,
    staleTime: 5 * 60_000,
    retry: false,
  })
  const authUser = useAuthStore((state) => state.auth.user)
  const clearPrivacyNoticeTimer = useCallback(() => {
    if (privacyNoticeTimerRef.current === null) return
    clearTimeout(privacyNoticeTimerRef.current)
    privacyNoticeTimerRef.current = null
  }, [])
  const schedulePrivacyNoticeCollapse = useCallback(() => {
    clearPrivacyNoticeTimer()
    setPrivacyNoticeExpanded(true)
    privacyNoticeTimerRef.current = setTimeout(() => {
      privacyNoticeTimerRef.current = null
      setPrivacyNoticeExpanded(false)
    }, ASSISTANT_PRIVACY_NOTICE_COLLAPSE_DELAY_MS)
  }, [clearPrivacyNoticeTimer])
  useEffect(() => {
    if (!panelVisible || mode === 'page') {
      clearPrivacyNoticeTimer()
      return
    }

    schedulePrivacyNoticeCollapse()
    return clearPrivacyNoticeTimer
  }, [
    clearPrivacyNoticeTimer,
    mode,
    panelVisible,
    schedulePrivacyNoticeCollapse,
  ])
  const togglePrivacyNotice = useCallback(() => {
    if (privacyNoticeExpanded) {
      clearPrivacyNoticeTimer()
      setPrivacyNoticeExpanded(false)
      return
    }
    schedulePrivacyNoticeCollapse()
  }, [
    clearPrivacyNoticeTimer,
    privacyNoticeExpanded,
    schedulePrivacyNoticeCollapse,
  ])
  const isAdministrator = statusQuery.data?.is_admin === true
  const accessLevel = statusQuery.data?.access_level
  const withAccessLevel = (message: string) =>
    accessLevel ? `${accessLevel} · ${message}` : message
  const superAdministratorFunded =
    statusQuery.data?.funding?.mode === 'super_administrator'
  const connectionModelsQuery = useQuery({
    queryKey: ['assistant-available-models'],
    queryFn: getAssistantAvailableModels,
    enabled:
      props.open &&
      developerAccessGranted &&
      (activeTool === 'key' || activeTool === 'setup'),
    staleTime: 60_000,
    retry: false,
  })
  const accountToolActive = activeTool !== null
  const historyVisible = historyView !== null

  const openAssistantTool = useCallback((tool: AssistantTool) => {
    setActiveTool(tool)
    window.requestAnimationFrame(() => {
      activeToolRegionRef.current?.focus({ preventScroll: true })
      activeToolRegionRef.current?.scrollIntoView({
        behavior: 'smooth',
        block: 'nearest',
      })
    })
  }, [])

  let assistantFooterStatus = t('Loading...')
  let assistantDescription = t('Loading...')
  let assistantPromptPlaceholder = t('Ask AI assistant')
  if (isAdministrator) {
    assistantFooterStatus = withAccessLevel(t('Administrator mode'))
    assistantDescription = t(
      'Administrator mode can inspect safe server settings and prepare model pricing changes for your confirmation.'
    )
    assistantPromptPlaceholder = t(
      'Ask about server configuration, model pricing, or operations...'
    )
  } else if (developerAccessGranted) {
    assistantFooterStatus = withAccessLevel(
      superAdministratorFunded
        ? t('Funded by the super administrator')
        : t('Loading...')
    )
    assistantDescription = t(
      'Guidance for plans, setup, API keys, costs, and support.'
    )
    assistantPromptPlaceholder = t('Ask about plans, setup, keys, or costs...')
  } else if (accountAccessState === 'restricted') {
    assistantFooterStatus = withAccessLevel(
      superAdministratorFunded
        ? t('Funded by the super administrator')
        : t('Read-only')
    )
    assistantDescription = t(
      'Guidance for plans, setup, API keys, costs, and support.'
    )
    assistantPromptPlaceholder = t('Ask AI assistant')
  } else if (accountAccessState === 'error') {
    assistantFooterStatus = withAccessLevel(
      t('Unable to verify account access')
    )
    assistantDescription = t('Unable to verify account access')
  }

  const clearToolState = useCallback(() => {
    setActiveTool(null)
    setRecommendationDraft(null)
    setAccountDisableDraft(null)
    setHumanSupportAction(null)
    setKeyCreationAction(null)
    setAutoConfirmKeyToken(null)
    setUserActionDraft(null)
  }, [])

  const clearTransientCards = useCallback(() => {
    clearToolState()
    setEntries((current) =>
      current.map((entry) =>
        entry.action || entry.adminChange || entry.imageAction
          ? {
              ...entry,
              action: undefined,
              adminChange: undefined,
              imageAction: undefined,
            }
          : entry
      )
    )
  }, [clearToolState])

  const resetConversation = useCallback(() => {
    assistantAbortControllerRef.current?.abort()
    setEntries([])
    setConversationId(null)
    setSelectedPreConversationPresetId(null)
    setConversationRestricted(false)
    clearToolState()
    setHistoryView(null)
    openedTargetRef.current = undefined
    setConversationResetRevision((revision) => revision + 1)
    onConversationReset?.()
  }, [clearToolState, onConversationReset])

  const continueHistoryConversation = useCallback(
    (detail: AssistantConversationHistoryDetail) => {
      const restoredEntries = detail.messages.flatMap<ConversationEntry>(
        (message) => {
          if (message.role !== 'user' && message.role !== 'assistant') return []
          const safeMessage = redactAssistantMessageForDisplay(
            message.content,
            t(
              'Sensitive details are hidden until confirmation and remain visible only to you.'
            )
          )
          return [
            {
              id: `history-${message.id}`,
              role: message.role,
              content: safeMessage.content,
            },
          ]
        }
      )
      setEntries(restoredEntries)
      setConversationId(detail.conversation.id)
      setConversationRestricted(Boolean(detail.conversation.restricted_at))
      clearToolState()
      setHistoryView(null)
    },
    [clearToolState, t]
  )

  const openAssistantTarget = useCallback(
    (target: AssistantPresetId) => {
      const restrictedTarget =
        target === 'onboarding' ||
        target === 'client-setup' ||
        target === 'bounty' ||
        target === 'cost' ||
        target === 'human'
      if (
        accountAccessState !== 'granted' &&
        !(accountAccessState === 'restricted' && restrictedTarget)
      ) {
        return false
      }

      const tool = getAssistantToolForTarget(target)
      const action = getAssistantActionForTarget(target, t)
      clearTransientCards()
      setActiveTool(tool)
      setEntries((current) => [
        ...current,
        {
          id: nanoid(),
          role: 'assistant',
          content: assistantDescription,
          action: tool ? undefined : action,
        },
      ])
      return true
    },
    [accountAccessState, assistantDescription, clearTransientCards, t]
  )

  useEffect(() => {
    const target = props.initialPreset
    if (!target || !accountAccessConfirmed) return
    if (openedTargetRef.current === target) return
    if (openAssistantTarget(target)) openedTargetRef.current = target
  }, [accountAccessConfirmed, openAssistantTarget, props.initialPreset])

  useEffect(
    () =>
      subscribeToAssistantOpen((request) => {
        const target = request.preset
        if (!target) return
        if (openAssistantTarget(target)) openedTargetRef.current = target
      }),
    [openAssistantTarget]
  )

  const handleOpenChange = (open: boolean) => {
    // Closing the mobile sheet should always return to the conversation on
    // the next open. Keep the conversation and active tool state intact, but
    // do not reopen a stale history list/detail view over it.
    if (!open && mode === 'mobile') setHistoryView(null)
    props.onOpenChange(open)
  }

  const requestAssistantReply = async (
    message: string,
    history: AssistantChatMessage[],
    presetId?: string
  ) => {
    setSending(true)
    const abortController = new AbortController()
    assistantAbortControllerRef.current = abortController
    const streamingEntryId = nanoid()
    let streamedContent = ''
    let streamFrame: number | null = null
    const displayRedactionNotice = t(
      'Sensitive details are hidden until confirmation and remain visible only to you.'
    )
    setEntries((current) => [
      ...current,
      { id: streamingEntryId, role: 'assistant', content: '' },
    ])
    const renderStreamedContent = () => {
      const safeContent = redactAssistantMessageForDisplay(
        streamedContent,
        displayRedactionNotice
      ).content
      setEntries((current) =>
        current.map((entry) =>
          entry.id === streamingEntryId
            ? { ...entry, content: safeContent }
            : entry
        )
      )
    }
    const scheduleStreamRender = () => {
      if (streamFrame !== null) return
      streamFrame = window.requestAnimationFrame(() => {
        streamFrame = null
        renderStreamedContent()
      })
    }
    try {
      const reply = await sendAssistantMessage(
        message,
        history,
        conversationId ?? undefined,
        presetId,
        {
          onDelta: (delta) => {
            streamedContent += delta
            scheduleStreamRender()
          },
          onReset: () => {
            if (streamFrame !== null) {
              window.cancelAnimationFrame(streamFrame)
              streamFrame = null
            }
            streamedContent = ''
            renderStreamedContent()
          },
        },
        abortController.signal
      )
      if (reply.conversationId) setConversationId(reply.conversationId)
      if (reply.restricted) setConversationRestricted(true)
      const safeReply = redactAssistantMessageForDisplay(
        reply.content,
        displayRedactionNotice
      )
      const suggestedTarget = getAssistantPresetForIntent(reply.intent)
      const explicitNavigation = getExplicitAssistantNavigation(
        message,
        reply.intent
      )
      const adminChange =
        reply.action?.type === 'admin_config_change' ||
        reply.action?.type === 'admin_pricing_change' ||
        reply.action?.type === 'admin_model_sync'
          ? reply.action
          : undefined
      const imageAction =
        reply.action?.type === 'image_generation' ? reply.action : undefined
      const humanSupportAction =
        reply.action?.type === 'human_support' ? reply.action : undefined
      const userAction =
        reply.action?.type === 'user_password_change' ||
        reply.action?.type === 'user_oauth_unbind' ||
        reply.action?.type === 'user_account_action'
          ? reply.action
          : undefined
      let suggestedAction: AssistantAction | undefined
      const restrictedTargetAllowed =
        accountAccessState === 'restricted' &&
        suggestedTarget !== undefined &&
        (suggestedTarget !== 'onboarding' ||
          isExplicitAssistantL1Request(message)) &&
        [
          'onboarding',
          'client-setup',
          'cost',
          'bounty',
          'plan',
          'human',
        ].includes(suggestedTarget)
      if (developerAccessGranted || restrictedTargetAllowed) {
        suggestedAction = getAssistantActionForTarget(suggestedTarget, t)
      }
      if (imageAction) {
        setRecommendationDraft(null)
        setAccountDisableDraft(null)
        setHumanSupportAction(null)
        setKeyCreationAction(null)
        setUserActionDraft(null)
        setActiveTool(null)
        suggestedAction = undefined
      } else if (adminChange) {
        setRecommendationDraft(null)
        setAccountDisableDraft(null)
        setHumanSupportAction(null)
        setKeyCreationAction(null)
        setUserActionDraft(null)
        setActiveTool(null)
        suggestedAction = undefined
      } else if (reply.action?.type === 'navigate') {
        setRecommendationDraft(null)
        setAccountDisableDraft(null)
        setHumanSupportAction(null)
        setUserActionDraft(null)
        setActiveTool(null)
        suggestedAction = {
          kind: 'navigation',
          label: assistantNavigationLabel(reply.action, t),
          href: assistantNavigationHref(reply.action),
        }
      } else if (reply.action?.type === 'l1_recommendation') {
        setRecommendationDraft(reply.action)
        setAccountDisableDraft(null)
        setHumanSupportAction(null)
        setUserActionDraft(null)
        setActiveTool('activation')
        suggestedAction = {
          kind: 'tool',
          label: t('Review AI recommendation'),
          tool: 'activation',
        }
      } else if (reply.action?.type === 'account_disable_request') {
        setAccountDisableDraft(reply.action)
        setHumanSupportAction(null)
        setRecommendationDraft(null)
        setKeyCreationAction(null)
        setUserActionDraft(null)
        setActiveTool(null)
        suggestedAction = undefined
      } else if (reply.action?.type === 'create_key') {
        setKeyCreationAction(reply.action)
        setHumanSupportAction(null)
        setRecommendationDraft(null)
        setAccountDisableDraft(null)
        setUserActionDraft(null)
        setActiveTool('key')
        suggestedAction = undefined
      } else if (userAction) {
        setRecommendationDraft(null)
        setAccountDisableDraft(null)
        setHumanSupportAction(null)
        setKeyCreationAction(null)
        setUserActionDraft(userAction)
        setActiveTool(null)
        suggestedAction = undefined
      } else if (humanSupportAction) {
        setRecommendationDraft(null)
        setAccountDisableDraft(null)
        setHumanSupportAction(humanSupportAction)
        setKeyCreationAction(null)
        setUserActionDraft(null)
        setActiveTool('handoff')
        suggestedAction = undefined
      }
      if (
        accountAccessState === 'restricted' &&
        isExplicitAssistantL1Request(message) &&
        !adminChange &&
        !imageAction &&
        !humanSupportAction &&
        !userAction &&
        reply.action?.type !== 'account_disable_request' &&
        reply.action?.type !== 'navigate' &&
        reply.action?.type !== 'create_key' &&
        reply.action?.type !== 'human_support'
      ) {
        setActiveTool('activation')
        suggestedAction ??= {
          kind: 'tool',
          label: t('Submit for administrator review'),
          tool: 'activation',
        }
      }
      const completedEntry: ConversationEntry = {
        id: streamingEntryId,
        role: 'assistant',
        content: safeReply.content,
        tools: reply.tools,
        action: suggestedAction,
        adminChange,
        imageAction,
      }
      setEntries((current) => {
        const found = current.some((entry) => entry.id === streamingEntryId)
        if (!found) return [...current, completedEntry]
        return current.map((entry) =>
          entry.id === streamingEntryId ? completedEntry : entry
        )
      })
      if (explicitNavigation && developerAccessGranted) {
        void navigate({ to: explicitNavigation })
      }
      await queryClient.invalidateQueries({ queryKey: ['assistant-status'] })
      await queryClient.invalidateQueries({ queryKey: ['assistant-journey'] })
      await queryClient.invalidateQueries({
        queryKey: ['assistant-new-user-gift'],
      })
      await queryClient.invalidateQueries({
        queryKey: ['assistant-weekly-discount'],
      })
      await queryClient.invalidateQueries({
        queryKey: ['assistant-conversations'],
      })
    } catch (error) {
      if (isAssistantRequestAborted(error)) {
        if (streamFrame !== null) {
          window.cancelAnimationFrame(streamFrame)
          streamFrame = null
        }
        if (streamedContent) {
          renderStreamedContent()
        } else {
          setEntries((current) =>
            current.map((entry) =>
              entry.id === streamingEntryId
                ? { ...entry, content: t('Response stopped.'), notice: true }
                : entry
            )
          )
        }
        return
      }
      const canSubmitWithoutAssistant =
        accountAccessState === 'restricted' &&
        isExplicitAssistantL1Request(message)
      if (canSubmitWithoutAssistant) {
        setRecommendationDraft(null)
        setActiveTool('activation')
      }
      let errorAction: AssistantAction | undefined
      if (canSubmitWithoutAssistant) {
        errorAction = {
          kind: 'tool',
          label: t('Submit for administrator review'),
          tool: 'activation',
        }
      } else if (developerAccessGranted) {
        errorAction = {
          kind: 'route',
          label: t('Contact support'),
          to: '/support',
        }
      }
      const errorEntry: ConversationEntry = {
        id: nanoid(),
        role: 'assistant',
        content: assistantFailureMessage(error, t),
        error: true,
        retry: { message, history, presetId },
        action: errorAction,
      }
      setEntries((current) => [
        ...current.filter((entry) => entry.id !== streamingEntryId),
        errorEntry,
      ])
    } finally {
      if (streamFrame !== null) window.cancelAnimationFrame(streamFrame)
      if (assistantAbortControllerRef.current === abortController) {
        assistantAbortControllerRef.current = null
      }
      setSending(false)
    }
  }

  const submitMessage = async ({ text }: { text?: string }) => {
    const message = text?.trim()
    if (sending || conversationRestricted) return
    if (!message) {
      throw new Error(t('Please enter a message.'))
    }
    const validation = getAssistantPromptValidation(message)
    if (validation.invalid) {
      throw new Error(
        t('Please enter a message other than a single punctuation mark.')
      )
    }
    const pendingKeyConfirmation =
      keyCreationAction !== null && isAssistantKeyConfirmationMessage(message)
    if (!pendingKeyConfirmation) clearTransientCards()
    const safeMessage = redactAssistantMessageForRequest(message)
    if (!hasAssistantMessageSubstantialMeaning(safeMessage.content)) {
      setEntries((current) => [
        ...current,
        {
          id: nanoid(),
          role: 'assistant',
          content: t(
            'Only sensitive content remained after redaction. Add a question without including the secret.'
          ),
          notice: true,
        },
      ])
      return
    }
    const history: AssistantChatMessage[] = entries
      .filter((entry) => !entry.error && !entry.notice)
      .map((entry) => ({ role: entry.role, content: entry.content }))
    setEntries((current) => [
      ...current,
      { id: nanoid(), role: 'user', content: safeMessage.content },
      ...(safeMessage.redacted
        ? [
            {
              id: nanoid(),
              role: 'assistant' as const,
              content: t('Sensitive content was redacted before sending.'),
              notice: true,
            },
          ]
        : []),
    ])
    if (pendingKeyConfirmation && keyCreationAction) {
      setAutoConfirmKeyToken(keyCreationAction.confirmation_token)
      setActiveTool('key')
      setSelectedPreConversationPresetId(null)
      return
    }
    const presetId = selectedPreConversationPresetId ?? undefined
    setSelectedPreConversationPresetId(null)
    await requestAssistantReply(safeMessage.content, history, presetId)
  }

  useEffect(() => {
    const requestId = props.autoSendRequestId
    const message = props.initialMessage?.trim()
    if (
      !requestId ||
      !message ||
      !accountAccessConfirmed ||
      submittedAutoSendIdRef.current === requestId
    ) {
      return
    }
    submittedAutoSendIdRef.current = requestId
    props.onAutoSendConsumed?.(requestId)
    void submitMessage({ text: message })
    // The request id is a single-use delivery token. Keeping it in a ref
    // prevents React StrictMode from submitting it again.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    accountAccessConfirmed,
    props.autoSendRequestId,
    props.initialMessage,
    props.onAutoSendConsumed,
  ])

  const retryMessage = async (entry: ConversationEntry) => {
    if (!entry.retry || sending) return
    setEntries((current) => current.filter((item) => item.id !== entry.id))
    await requestAssistantReply(
      entry.retry.message,
      entry.retry.history,
      entry.retry.presetId
    )
  }

  const panelContent = (
    <>
      <AssistantPanelHeader
        mode={mode}
        description={assistantDescription}
        classicLayout={classicLayout}
        onNewConversation={resetConversation}
        onToggleClassicLayout={() => setClassicLayout((value) => !value)}
        historyVisible={historyVisible}
        historyDetail={historyView !== null && historyView !== 'list'}
        onOpenHistory={() => setHistoryView('list')}
        onCloseHistory={() =>
          setHistoryView((current) =>
            current !== null && current !== 'list' ? 'list' : null
          )
        }
        onToggleCollapsed={props.onToggleCollapsed}
        fullscreen={props.fullscreen}
        onToggleFullscreen={props.onToggleFullscreen}
      />
      <Alert
        id='assistant-privacy-notice'
        className={cn(
          'mb-0 min-w-0 overflow-hidden',
          mode === 'page' ? 'hidden' : 'm-3 max-w-[calc(100%-1.5rem)]',
          classicLayout &&
            'border-[#565869] bg-[#2a2b32] text-[#ececf1] [&_p]:text-[#c5c5d2]',
          !classicLayout &&
            'border-0 bg-transparent px-0 text-muted-foreground shadow-none [&_[data-slot=alert-title]]:text-foreground',
          !privacyNoticeExpanded && 'py-1.5'
        )}
        data-testid='assistant-privacy-notice'
        variant='default'
      >
        <HugeiconsIcon icon={Alert02Icon} strokeWidth={2} aria-hidden='true' />
        <div className='min-w-0'>
          <AlertTitle className='min-w-0'>
            <button
              type='button'
              className='focus-visible:ring-ring/50 rounded-sm text-left font-medium outline-none focus-visible:ring-3'
              aria-controls='assistant-privacy-notice-description'
              aria-describedby='assistant-privacy-notice-description'
              aria-expanded={privacyNoticeExpanded}
              data-testid='assistant-privacy-notice-toggle'
              onClick={togglePrivacyNotice}
            >
              {t('Conversation privacy notice')}
            </button>
          </AlertTitle>
          <AlertDescription
            id='assistant-privacy-notice-description'
            className={privacyNoticeExpanded ? undefined : 'sr-only'}
          >
            <p className='break-words'>
              {t(
                'Your assistant conversations are not private. Authorized higher-access users may review them.'
              )}
            </p>
            <p className='break-words'>
              {t(
                'Do not send personal information, passwords, API keys, or credentials in chat. Site-issued credentials are shown only after your explicit confirmation, remain visible only to you, and stay out of the assistant context.'
              )}
            </p>
            <p className='break-words'>
              {t(
                'If you accidentally send supported sensitive data, the assistant safety filter may detect common email addresses, phone numbers, and API key formats and redact the message before this assistant request is sent. Pattern matching is not a guarantee.'
              )}
            </p>
          </AlertDescription>
        </div>
      </Alert>
      {historyVisible ? (
        <Conversation
          className={cn(
            'min-h-0 min-w-0 flex-1',
            classicLayout ? 'bg-[#343541] text-[#ececf1]' : 'bg-muted/20'
          )}
        >
          <ConversationContent
            className={cn(
              'flex min-h-full min-w-0 flex-col gap-5 overflow-x-hidden px-4 py-5 sm:px-6',
              mode === 'page' ? 'mx-auto w-full max-w-3xl' : 'max-w-full',
              classicLayout && 'text-[#ececf1]'
            )}
          >
            {historyView === 'list' ? (
              <AssistantHistory
                active={panelVisible}
                presentation='rows'
                showFullPageLink={isConsoleActivated(authUser)}
                onOpenConversation={(conversation) =>
                  setHistoryView(conversation)
                }
              />
            ) : (
              <AssistantHistoryConversation
                conversation={historyView}
                onContinue={continueHistoryConversation}
              />
            )}
          </ConversationContent>
        </Conversation>
      ) : (
        <>
          {assistantRouteUnavailable ? (
            <AssistantRouteStatusNotice
              onRetry={() => void statusQuery.refetch()}
            />
          ) : null}
          {developerAccessGranted && authUser ? (
            <AssistantOnboardingTodo
              userId={authUser.id}
              enabled={developerAccessGranted}
              presentation={mode === 'page' ? 'compact' : 'default'}
              onOpenKey={() => {
                clearToolState()
                setActiveTool('key')
              }}
              onOpenSetup={() => {
                clearToolState()
                setActiveTool('setup')
              }}
            />
          ) : null}
          <Conversation
            className={cn(
              'min-h-0 min-w-0 flex-1',
              classicLayout ? 'bg-[#343541] text-[#ececf1]' : 'bg-muted/20'
            )}
          >
            <ConversationContent
              className={cn(
                'flex min-h-full min-w-0 flex-col gap-5 overflow-x-hidden px-4 py-5 sm:px-6',
                classicLayout && 'gap-0 px-0 py-0 text-[#ececf1] sm:px-0',
                mode === 'page' ? 'mx-auto w-full max-w-3xl' : 'max-w-full'
              )}
            >
              {entries.length === 0 ? (
                <div
                  className={cn(
                    'flex min-h-0 flex-1 flex-col',
                    mode === 'page' && 'justify-center'
                  )}
                >
                  {accountAccessState === 'loading' ||
                  accountAccessState === 'error' ? (
                    <AssistantAccountStatusNotice
                      state={
                        accountAccessState === 'error' ? 'error' : 'loading'
                      }
                      onRetry={() => void statusQuery.refetch()}
                    />
                  ) : null}
                  <div
                    className='flex min-h-0 flex-1 flex-col'
                    data-testid='assistant-l0-welcome'
                  >
                    {classicLayout ? (
                      <AssistantClassicWelcome />
                    ) : (
                      <AssistantModernWelcome
                        description={assistantDescription}
                        restricted={accountAccessState === 'restricted'}
                      />
                    )}
                  </div>
                </div>
              ) : (
                <>
                  {entries.map((entry) => (
                    <Message
                      from={entry.role}
                      key={entry.id}
                      className={cn(
                        classicLayout &&
                          'assistant-classic-message mx-auto w-full max-w-3xl items-start px-5 py-5 sm:px-8 sm:py-6',
                        classicLayout &&
                          entry.role === 'user' &&
                          'justify-end bg-[#343541]',
                        classicLayout &&
                          entry.role === 'assistant' &&
                          'justify-start bg-[#444654]'
                      )}
                      data-testid={
                        classicLayout ? 'assistant-classic-message' : undefined
                      }
                    >
                      <MessageContent
                        variant='flat'
                        className={cn(
                          entry.error
                            ? 'text-destructive max-w-full min-w-0 gap-3 text-sm leading-6'
                            : 'max-w-full min-w-0 gap-3 text-sm leading-6',
                          classicLayout &&
                            entry.role === 'user' &&
                            'max-w-[min(86%,48rem)] rounded-2xl bg-[#2a2b32] px-4 py-3 text-[#ececf1] shadow-none',
                          classicLayout &&
                            entry.role === 'assistant' &&
                            'w-full max-w-3xl rounded-none bg-transparent px-0 py-0 text-[#ececf1]'
                        )}
                      >
                        {classicLayout && entry.role === 'assistant' ? (
                          <div className='mb-3 flex items-center gap-2 text-xs font-medium text-[#f1f1f1]'>
                            <LmmBrandMark className='size-6' />
                            <span>LMM Forge</span>
                          </div>
                        ) : null}
                        {entry.role === 'assistant' ? (
                          <Response
                            className='max-w-full leading-7 break-words [&_pre]:max-w-full [&_pre]:overflow-x-auto'
                            final
                          >
                            {entry.content}
                          </Response>
                        ) : (
                          <p className='break-words whitespace-pre-wrap'>
                            {entry.content}
                          </p>
                        )}
                        {entry.tools?.length ? (
                          <AssistantToolCalls traces={entry.tools} />
                        ) : null}
                        {entry.imageAction ? (
                          <AssistantImageTool action={entry.imageAction} />
                        ) : null}
                        {entry.adminChange ? (
                          <AssistantAdminChangeTool
                            action={entry.adminChange}
                            onApplied={() => {
                              void statusQuery.refetch()
                            }}
                          />
                        ) : null}
                        {entry.retry || entry.action ? (
                          <div className='flex flex-wrap gap-2'>
                            {entry.retry ? (
                              <Button
                                type='button'
                                variant='outline'
                                onClick={() => void retryMessage(entry)}
                                disabled={sending}
                              >
                                <HugeiconsIcon
                                  icon={ReloadIcon}
                                  strokeWidth={2}
                                  data-icon='inline-start'
                                  aria-hidden='true'
                                />
                                {t('Retry')}
                              </Button>
                            ) : null}
                            {entry.action ? (
                              <AssistantActionButton
                                action={entry.action}
                                onToolOpen={openAssistantTool}
                              />
                            ) : null}
                          </div>
                        ) : null}
                      </MessageContent>
                    </Message>
                  ))}
                  {sending ? (
                    <Message from='assistant'>
                      <MessageContent
                        variant='flat'
                        className='text-muted-foreground flex-row items-center gap-2'
                        aria-live='polite'
                      >
                        <Loader size={14} />
                        <span>{t('Assistant is thinking...')}</span>
                      </MessageContent>
                    </Message>
                  ) : null}
                  <AssistantNewUserGift enabled={accountAccessConfirmed} />
                  <AssistantWeeklyDiscount enabled={accountAccessConfirmed} />
                  <div
                    ref={activeToolRegionRef}
                    className={cn(
                      'grid gap-5 outline-none',
                      classicLayout &&
                        'mx-auto w-full max-w-3xl px-5 py-5 sm:px-8'
                    )}
                    data-testid='assistant-active-tool-region'
                    tabIndex={-1}
                  >
                    {accountToolActive && !accountAccessConfirmed ? (
                      <AssistantAccountStatusNotice
                        state={
                          accountAccessState === 'error' ? 'error' : 'loading'
                        }
                        onRetry={() => void statusQuery.refetch()}
                      />
                    ) : null}
                    {activeTool === 'key' && developerAccessGranted ? (
                      <AssistantKeyTool
                        baseUrl={baseUrl}
                        availableModels={connectionModelsQuery.data ?? []}
                        modelsLoading={connectionModelsQuery.isLoading}
                        developerAccessGranted={developerAccessGranted}
                        confirmationAction={keyCreationAction}
                        autoConfirm={
                          autoConfirmKeyToken ===
                          keyCreationAction?.confirmation_token
                        }
                        onKeyCreated={() => {
                          setAutoConfirmKeyToken(null)
                          setKeyCreationAction(null)
                          if (authUser) {
                            void queryClient.invalidateQueries({
                              queryKey: [
                                'assistant-onboarding-todo',
                                authUser.id,
                              ],
                            })
                          }
                        }}
                        onContinueSetup={() => setActiveTool('setup')}
                      />
                    ) : null}
                    {activeTool === 'activation' && accountAccessConfirmed ? (
                      <AssistantActivationTool
                        recommendationDraft={recommendationDraft}
                        onDraftConsumed={() => setRecommendationDraft(null)}
                        onContinueSetup={() => setActiveTool('setup')}
                        onSubmitted={() => {
                          setRecommendationDraft(null)
                          setEntries((current) => [
                            ...current,
                            {
                              id: nanoid(),
                              role: 'assistant',
                              content: t(
                                'Your AI recommendation was submitted to the automatic review agent. L1 remains locked until automatic review approves it or human fallback completes.'
                              ),
                            },
                          ])
                        }}
                      />
                    ) : null}
                    {accountDisableDraft ? (
                      <AssistantAccountActionTool
                        action={accountDisableDraft}
                        onSubmitted={() => setAccountDisableDraft(null)}
                      />
                    ) : null}
                    {userActionDraft ? (
                      <AssistantUserActionTool
                        action={userActionDraft}
                        onCompleted={() => setUserActionDraft(null)}
                      />
                    ) : null}
                    {activeTool === 'cost' && accountAccessConfirmed ? (
                      <AssistantCostTool
                        developerAccessGranted={developerAccessGranted}
                      />
                    ) : null}
                    {activeTool === 'handoff' && accountAccessConfirmed ? (
                      <AssistantHandoffTool
                        confirmationAction={humanSupportAction}
                      />
                    ) : null}
                    {activeTool === 'models' && developerAccessGranted ? (
                      <AssistantModelsTool />
                    ) : null}
                    {activeTool === 'plan' && accountAccessConfirmed ? (
                      <AssistantPlanTool
                        developerAccessGranted={developerAccessGranted}
                        onRequestAccess={() => setActiveTool('activation')}
                      />
                    ) : null}
                    {activeTool === 'setup' && accountAccessConfirmed ? (
                      <AssistantSetupTool
                        rootUrl={baseUrl.replace(/\/v1$/, '')}
                        openAIBaseUrl={baseUrl}
                        availableModels={connectionModelsQuery.data ?? []}
                        modelsLoading={connectionModelsQuery.isLoading}
                        developerAccessGranted={developerAccessGranted}
                        onCreateKey={() => setActiveTool('key')}
                        onRequestAccess={() => setActiveTool('activation')}
                      />
                    ) : null}
                    {activeTool === 'usage' && developerAccessGranted ? (
                      <AssistantUsageTool
                        developerAccessGranted={developerAccessGranted}
                      />
                    ) : null}
                  </div>

                  <div
                    className={cn(
                      'grid gap-3 pt-1',
                      classicLayout &&
                        'mx-auto w-full max-w-3xl px-5 pb-8 sm:px-8'
                    )}
                  >
                    <Separator
                      className={classicLayout ? 'bg-[#4b4d56]' : undefined}
                    />
                    <Button
                      type='button'
                      variant='ghost'
                      size='sm'
                      onClick={resetConversation}
                      disabled={sending}
                    >
                      <HugeiconsIcon
                        icon={CleanIcon}
                        strokeWidth={2}
                        data-icon='inline-start'
                        aria-hidden='true'
                      />
                      {t('Clear conversation')}
                    </Button>
                  </div>
                </>
              )}
            </ConversationContent>
            <ConversationScrollButton />
          </Conversation>

          <div
            className={cn(
              'min-w-0 shrink-0 overflow-hidden pb-[max(0.5rem,env(safe-area-inset-bottom))] sm:pb-[max(0.75rem,env(safe-area-inset-bottom))]',
              classicLayout
                ? 'bg-[#343541] text-[#ececf1]'
                : 'bg-background/95 supports-[backdrop-filter]:bg-background/85 backdrop-blur'
            )}
            data-testid='assistant-composer-footer'
          >
            <Separator
              className={classicLayout ? 'bg-[#4b4d56]' : 'bg-border/70'}
            />
            <div
              className={cn(
                'assistant-composer px-3 py-2 sm:px-4 sm:py-3',
                mode === 'page' && 'mx-auto w-full max-w-3xl',
                classicLayout && 'px-5 py-4 sm:px-8 sm:py-5'
              )}
            >
              <PromptInputProvider
                key={conversationResetRevision}
                initialInput={props.initialMessage}
              >
                <AssistantPromptInputSync
                  initialMessage={props.initialMessage}
                  initialMessageRevision={props.initialMessageRevision}
                />
                {accountAccessConfirmed &&
                !entries.some((entry) => entry.role === 'user') ? (
                  <AssistantPresetPrompts
                    presets={preConversationPresetsQuery.data?.presets ?? []}
                    onSelect={(preset) => {
                      setSelectedPreConversationPresetId(preset.id)
                      void recordAssistantPreConversationPresetClick(
                        preset.id
                      ).catch(() => undefined)
                    }}
                  />
                ) : null}
                <AssistantPromptComposer
                  footerStatus={
                    conversationRestricted
                      ? t('Conversation ended by safety policy')
                      : assistantFooterStatus
                  }
                  placeholder={assistantPromptPlaceholder}
                  privacyNoticeId='assistant-privacy-notice'
                  classicLayout={classicLayout}
                  restricted={accountAccessState === 'restricted'}
                  terminated={conversationRestricted}
                  routeUnavailable={assistantRouteUnavailable}
                  sending={sending}
                  onStop={() => assistantAbortControllerRef.current?.abort()}
                  onSubmit={submitMessage}
                />
              </PromptInputProvider>
            </div>
          </div>
        </>
      )}
    </>
  )

  if (mode === 'page') {
    return (
      <section
        id='ai-assistant-panel'
        className={cn(
          'bg-background flex min-h-0 min-w-0 flex-1',
          classicLayout && 'assistant-classic-shell bg-[#343541] text-[#ececf1]'
        )}
        data-layout={classicLayout ? 'classic' : 'modern'}
        aria-label={t('Service guide')}
      >
        {classicLayout ? (
          <AssistantClassicSidebar
            onNewConversation={resetConversation}
            onOpenHistory={() => setHistoryView('list')}
            onToggleLayout={() => setClassicLayout(false)}
          />
        ) : null}
        <main
          className={cn(
            'flex min-h-0 min-w-0 flex-1 flex-col',
            classicLayout && 'bg-[#343541]'
          )}
        >
          {panelContent}
        </main>
      </section>
    )
  }

  if (mode === 'rail') {
    if (props.fullscreen) {
      return (
        <div
          id='ai-assistant-panel'
          role='dialog'
          aria-modal='true'
          aria-label={t('Service guide')}
          className={cn(
            'fixed inset-0 z-50 flex min-h-0 flex-col',
            classicLayout
              ? 'assistant-classic-shell bg-[#343541] text-[#ececf1]'
              : 'bg-background'
          )}
          data-layout={classicLayout ? 'classic' : 'modern'}
        >
          {panelContent}
        </div>
      )
    }
    if (props.collapsed) {
      return (
        <aside
          id='ai-assistant-panel'
          className={cn(
            'hidden min-h-0 w-12 shrink-0 flex-col border-l xl:flex',
            classicLayout
              ? 'assistant-classic-shell border-[#4b4d56] bg-[#343541] text-[#ececf1]'
              : 'bg-background'
          )}
          data-layout={classicLayout ? 'classic' : 'modern'}
          aria-label={t('Service guide')}
        >
          <Button
            type='button'
            variant='ghost'
            size='icon'
            className='m-2'
            aria-label={t('Expand')}
            title={t('Expand')}
            data-testid='assistant-expand'
            onClick={props.onToggleCollapsed}
          >
            <HugeiconsIcon
              icon={ArrowRight01Icon}
              strokeWidth={2}
              aria-hidden='true'
            />
            <span className='sr-only'>{t('Expand')}</span>
          </Button>
          <span className='sr-only'>{t('Service guide')}</span>
        </aside>
      )
    }

    return (
      <aside
        id='ai-assistant-panel'
        className={cn(
          'hidden min-h-0 w-[min(28vw,30rem)] max-w-full min-w-0 shrink-0 flex-col border-l xl:flex',
          classicLayout
            ? 'assistant-classic-shell border-[#4b4d56] bg-[#343541] text-[#ececf1]'
            : 'bg-background'
        )}
        data-layout={classicLayout ? 'classic' : 'modern'}
        aria-label={t('Service guide')}
      >
        {panelContent}
      </aside>
    )
  }

  return (
    <Sheet open={props.open} onOpenChange={handleOpenChange}>
      <SheetContent
        id='ai-assistant-panel'
        className={sideDrawerContentClassName(
          cn(
            'inset-0 !h-dvh !max-h-dvh !min-h-0 !w-screen !max-w-none !min-w-0 rounded-none overscroll-contain',
            classicLayout
              ? 'assistant-classic-shell bg-[#343541] text-[#ececf1]'
              : 'bg-background'
          )
        )}
        data-layout={classicLayout ? 'classic' : 'modern'}
      >
        {panelContent}
      </SheetContent>
    </Sheet>
  )
}
