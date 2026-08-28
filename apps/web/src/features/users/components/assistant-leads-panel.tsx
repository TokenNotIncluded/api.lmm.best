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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Check,
  CheckCircle2,
  CircleAlert,
  MessageSquareText,
  RefreshCw,
} from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import {
  Alert,
  AlertAction,
  AlertDescription,
  AlertTitle,
} from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Empty,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from '@/components/ui/empty'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import {
  getAssistantFirstQuestionSummary,
  getAssistantFundingSummary,
  getAssistantIntentSummary,
  getAssistantProfileSummary,
  listAssistantHandoffs,
  resolveAssistantHandoff,
  type AssistantHandoff,
  type AssistantFirstQuestionSummary,
  type AssistantIntentSummary,
  type AssistantProfileSummary,
} from '@/features/assistant/api'
import { toIntlLocale } from '@/i18n/languages'
import { formatPlatformAmount } from '@/lib/currency'

const PENDING_HANDOFFS_QUERY_KEY = [
  'assistant-admin-handoffs',
  'pending',
] as const
const RESOLVED_HANDOFFS_QUERY_KEY = [
  'assistant-admin-handoffs',
  'resolved',
] as const
const INTENT_SUMMARY_QUERY_KEY = ['assistant-admin-intents', 30] as const
const PROFILE_SUMMARY_QUERY_KEY = ['assistant-admin-profiles', 30] as const
const FIRST_QUESTION_SUMMARY_QUERY_KEY = [
  'assistant-admin-first-questions',
  30,
] as const
const FUNDING_SUMMARY_QUERY_KEY = ['assistant-admin-funding', 30] as const
const EMPTY_INTENTS: AssistantIntentSummary[] = []
const EMPTY_PROFILES: AssistantProfileSummary[] = []
const EMPTY_FIRST_QUESTIONS: AssistantFirstQuestionSummary[] = []

const INTENT_LABELS: Record<string, string> = {
  onboarding: 'Onboarding and L1',
  plan_purchase: 'Plans and purchase',
  api_key: 'API keys',
  client_setup: 'Client setup',
  cost: 'Cost calculation',
  math: 'Math calculation',
  recommendation: 'Recommendation letter',
  bounty: 'Open-source bounties',
  usage: 'Usage',
  models: 'Models',
  invitation: 'Invitation rewards',
  human_support: 'Human support',
  other: 'Other questions',
}

const PROFILE_LABELS: Record<string, string> = {
  technical_cost_sensitive: 'Technical cost-sensitive',
  guided_buyer: 'Guided buyer',
  promotion_seeker: 'Promotion seeker',
  security_risk: 'Security-sensitive',
  production_operator: 'Production operator',
  privacy_conscious: 'Privacy-conscious',
  mobile_accessibility: 'Mobile accessibility',
  normal_user: 'Normal user',
  unknown: 'Insufficient signals',
}

function isNotFound(error: unknown): boolean {
  return (
    (error as { response?: { status?: number } } | null)?.response?.status ===
    404
  )
}

function HandoffsSkeleton() {
  return (
    <div className='grid min-w-0 gap-3' aria-hidden='true'>
      {[1, 2].map((key) => (
        <div
          key={key}
          className='border-border min-w-0 space-y-3 border-b py-6'
        >
          <div className='flex min-w-0 justify-between gap-3'>
            <div className='min-w-0 space-y-2'>
              <Skeleton className='h-4 w-28 max-w-full' />
              <Skeleton className='h-3 w-52 max-w-full' />
            </div>
            <Skeleton className='h-5 w-16 shrink-0' />
          </div>
          <Skeleton className='h-12 w-full' />
        </div>
      ))}
    </div>
  )
}

function EmptyHandoffs(props: {
  icon: typeof MessageSquareText
  title: string
}) {
  const { icon: Icon, title } = props
  return (
    <Empty className='min-w-0 border-0 py-12'>
      <EmptyHeader>
        <EmptyMedia variant='icon'>
          <Icon aria-hidden='true' />
        </EmptyMedia>
        <EmptyTitle>{title}</EmptyTitle>
      </EmptyHeader>
    </Empty>
  )
}

export function AssistantLeadsPanel() {
  const { t, i18n } = useTranslation()
  const intlLocale = toIntlLocale(i18n.language)
  const queryClient = useQueryClient()
  const [notes, setNotes] = useState<Record<number, string>>({})

  const pendingQuery = useQuery({
    queryKey: PENDING_HANDOFFS_QUERY_KEY,
    queryFn: () => listAssistantHandoffs('pending'),
    staleTime: 30_000,
    retry: false,
  })
  const resolvedQuery = useQuery({
    queryKey: RESOLVED_HANDOFFS_QUERY_KEY,
    queryFn: () => listAssistantHandoffs('resolved'),
    staleTime: 30_000,
    retry: false,
  })
  const intentsQuery = useQuery({
    queryKey: INTENT_SUMMARY_QUERY_KEY,
    queryFn: () => getAssistantIntentSummary(30),
    staleTime: 30_000,
    retry: false,
  })
  const profilesQuery = useQuery({
    queryKey: PROFILE_SUMMARY_QUERY_KEY,
    queryFn: () => getAssistantProfileSummary(30),
    staleTime: 30_000,
    retry: false,
  })
  const firstQuestionsQuery = useQuery({
    queryKey: FIRST_QUESTION_SUMMARY_QUERY_KEY,
    queryFn: () => getAssistantFirstQuestionSummary(30),
    staleTime: 30_000,
    retry: false,
  })
  const fundingQuery = useQuery({
    queryKey: FUNDING_SUMMARY_QUERY_KEY,
    queryFn: () => getAssistantFundingSummary(30),
    staleTime: 30_000,
    retry: false,
  })

  const pending = pendingQuery.data ?? []
  const resolved = resolvedQuery.data ?? []
  const intents = intentsQuery.data ?? EMPTY_INTENTS
  const profiles = profilesQuery.data ?? EMPTY_PROFILES
  const firstQuestions = firstQuestionsQuery.data ?? EMPTY_FIRST_QUESTIONS
  const funding = fundingQuery.data
  const numberFormatter = useMemo(
    () => new Intl.NumberFormat(intlLocale),
    [intlLocale]
  )
  const formatPlatformCost = (amount: number) =>
    formatPlatformAmount(
      amount,
      {
        locale: intlLocale,
        abbreviate: false,
        digitsLarge: 2,
        digitsSmall: 4,
      },
      t('Platform')
    )
  const totalIntents = useMemo(
    () => intents.reduce((total, item) => total + item.count, 0),
    [intents]
  )
  const totalProfiles = useMemo(
    () => profiles.reduce((total, item) => total + item.count, 0),
    [profiles]
  )
  const dateTimeFormatter = useMemo(
    () =>
      new Intl.DateTimeFormat(intlLocale, {
        dateStyle: 'medium',
        timeStyle: 'short',
      }),
    [intlLocale]
  )

  const resolveMutation = useMutation({
    mutationFn: ({
      handoff,
      note,
    }: {
      handoff: AssistantHandoff
      note: string
    }) => resolveAssistantHandoff(handoff.id, note),
    onSuccess: (updated, { handoff }) => {
      queryClient.setQueryData<AssistantHandoff[]>(
        PENDING_HANDOFFS_QUERY_KEY,
        (current = []) => current.filter((item) => item.id !== handoff.id)
      )
      queryClient.setQueryData<AssistantHandoff[]>(
        RESOLVED_HANDOFFS_QUERY_KEY,
        (current = []) => [
          {
            ...handoff,
            ...updated,
            username: updated.username ?? handoff.username,
            email: updated.email ?? handoff.email,
          },
          ...current.filter((item) => item.id !== handoff.id),
        ]
      )
      setNotes((current) => {
        const next = { ...current }
        delete next[handoff.id]
        return next
      })
      toast.success(t('Support task completed'))
      void Promise.all([
        queryClient.invalidateQueries({ queryKey: PENDING_HANDOFFS_QUERY_KEY }),
        queryClient.invalidateQueries({
          queryKey: RESOLVED_HANDOFFS_QUERY_KEY,
        }),
      ])
    },
    onError: (error) => {
      toast.error(
        error instanceof Error
          ? error.message
          : t('Unable to complete support task')
      )
    },
  })

  // The task queue is the primary surface. Optional insights must not hide it
  // when a mixed-version deployment does not expose their endpoints yet.
  const requiredQueries = [pendingQuery, resolvedQuery]
  if (requiredQueries.some((query) => isNotFound(query.error))) return null
  const queries = [
    ...requiredQueries,
    profilesQuery,
    firstQuestionsQuery,
    fundingQuery,
  ]

  const firstError = queries.find(
    (query) => query.isError && !isNotFound(query.error)
  )?.error
  const isRefreshing = queries.some((query) => query.isFetching)
  const refresh = () =>
    Promise.all(queries.map((query) => query.refetch())).then(() => undefined)

  const renderPending = () => {
    if (pendingQuery.isLoading) return <HandoffsSkeleton />
    if (pendingQuery.isError) {
      return (
        <EmptyHandoffs
          icon={CircleAlert}
          title={t('Pending support tasks are unavailable.')}
        />
      )
    }
    if (pending.length === 0) {
      return (
        <EmptyHandoffs
          icon={CheckCircle2}
          title={t('No pending support tasks.')}
        />
      )
    }
    return (
      <div className='min-w-0' data-testid='assistant-pending-task-list'>
        {pending.map((handoff) => {
          const isResolving =
            resolveMutation.isPending &&
            resolveMutation.variables?.handoff.id === handoff.id
          const createdAt = new Date(handoff.created_at * 1000)
          const noteId = `assistant-support-note-${handoff.id}`
          return (
            <article
              key={handoff.id}
              className='border-border min-w-0 overflow-hidden border-b py-7'
              data-testid={`assistant-pending-task-${handoff.id}`}
            >
              <div className='flex min-w-0 flex-col gap-2 sm:flex-row sm:items-start sm:justify-between'>
                <div className='min-w-0 flex-1'>
                  <p className='font-medium break-words'>
                    {handoff.username || t('Unknown user')}
                  </p>
                  <p className='text-muted-foreground min-w-0 text-xs break-words'>
                    <time dateTime={createdAt.toISOString()}>
                      {dateTimeFormatter.format(createdAt)}
                    </time>{' '}
                    · {handoff.email || t('No email provided')}
                  </p>
                </div>
                <Badge className='w-fit shrink-0' variant='outline'>
                  {t('Pending')}
                </Badge>
              </div>

              <div
                className='mt-5 max-w-3xl min-w-0'
                data-testid={`assistant-redacted-request-${handoff.id}`}
              >
                <p className='text-xs font-medium'>
                  {t('Privacy-minimized request')}
                </p>
                <p className='text-muted-foreground mt-1 break-words whitespace-pre-wrap'>
                  {handoff.message || t('No request details provided.')}
                </p>
              </div>

              <label
                className='mt-3 block text-xs font-medium'
                htmlFor={noteId}
              >
                {t('Processing note')}
              </label>
              <Textarea
                id={noteId}
                className='mt-2 max-w-3xl min-w-0 rounded-xl'
                rows={2}
                maxLength={2000}
                aria-label={t('Processing note')}
                placeholder={t('Optional note for the user')}
                value={notes[handoff.id] ?? ''}
                onChange={(event) =>
                  setNotes((current) => ({
                    ...current,
                    [handoff.id]: event.target.value,
                  }))
                }
              />
              <div className='mt-3 flex flex-col-reverse gap-2 sm:flex-row sm:justify-end'>
                <Button
                  className='w-full sm:w-auto'
                  size='sm'
                  aria-label={t('Complete support task')}
                  onClick={() =>
                    resolveMutation.mutate({
                      handoff,
                      note: notes[handoff.id] ?? '',
                    })
                  }
                  disabled={resolveMutation.isPending}
                >
                  {isResolving ? (
                    <RefreshCw
                      data-icon='inline-start'
                      className='animate-spin'
                      aria-hidden='true'
                    />
                  ) : (
                    <Check data-icon='inline-start' aria-hidden='true' />
                  )}
                  {isResolving ? t('Completing...') : t('Complete task')}
                </Button>
              </div>
            </article>
          )
        })}
      </div>
    )
  }

  const renderResolved = () => {
    if (resolvedQuery.isLoading) return <HandoffsSkeleton />
    if (resolvedQuery.isError) {
      return (
        <EmptyHandoffs
          icon={CircleAlert}
          title={t('Resolved support history is unavailable.')}
        />
      )
    }
    if (resolved.length === 0) {
      return (
        <EmptyHandoffs
          icon={CheckCircle2}
          title={t('No resolved support tasks.')}
        />
      )
    }
    return (
      <div
        className='max-h-[32rem] min-w-0 overflow-y-auto pr-1'
        data-testid='assistant-resolved-task-list'
      >
        {resolved.map((handoff) => {
          const createdAt = new Date(handoff.created_at * 1000)
          return (
            <article
              key={handoff.id}
              className='border-border min-w-0 overflow-hidden border-b py-4 last:border-b-0'
              data-testid={`assistant-resolved-task-${handoff.id}`}
            >
              <div className='flex min-w-0 flex-col gap-2 sm:flex-row sm:items-start sm:justify-between'>
                <div className='min-w-0 flex-1'>
                  <p className='font-medium break-words'>
                    {handoff.username || t('Unknown user')}
                  </p>
                  <p className='text-muted-foreground min-w-0 text-xs break-words'>
                    <time dateTime={createdAt.toISOString()}>
                      {dateTimeFormatter.format(createdAt)}
                    </time>{' '}
                    · {handoff.email || t('No email provided')}
                  </p>
                </div>
                <Badge className='w-fit shrink-0' variant='secondary'>
                  {t('Resolved')}
                </Badge>
              </div>
              <div className='mt-5 max-w-3xl min-w-0'>
                <p className='text-xs font-medium'>
                  {t('Privacy-minimized request')}
                </p>
                <p className='text-muted-foreground mt-1 break-words whitespace-pre-wrap'>
                  {handoff.message || t('No request details provided.')}
                </p>
              </div>
              <Separator className='my-3' />
              <p className='text-xs font-medium'>{t('Processing note')}</p>
              <p className='text-muted-foreground mt-1 break-words whitespace-pre-wrap'>
                {handoff.admin_note || t('No note provided.')}
              </p>
              {handoff.resolved_at > 0 ? (
                <p className='text-muted-foreground mt-2 text-xs'>
                  {t('Completed at')}:{' '}
                  <time
                    dateTime={new Date(
                      handoff.resolved_at * 1000
                    ).toISOString()}
                  >
                    {dateTimeFormatter.format(
                      new Date(handoff.resolved_at * 1000)
                    )}
                  </time>
                </p>
              ) : null}
            </article>
          )
        })}
      </div>
    )
  }

  const renderIntentInsights = () => {
    if (intentsQuery.isLoading) {
      return <Skeleton className='mt-3 h-5 w-36 max-w-full' />
    }
    if (intentsQuery.isError) {
      return (
        <p className='text-muted-foreground mt-3 text-sm'>
          {t('Unable to load intent insights')}
        </p>
      )
    }
    return (
      <div className='mt-3 flex min-w-0 flex-wrap gap-1.5'>
        <Badge variant='outline'>
          {t('{{count}} questions in 30 days', { count: totalIntents })}
        </Badge>
        {intents.map((item) => (
          <Badge key={item.intent} variant='secondary'>
            {t(INTENT_LABELS[item.intent] ?? 'Other questions')}: {item.count}
          </Badge>
        ))}
      </div>
    )
  }

  const renderProfileInsights = () => {
    if (profilesQuery.isLoading) {
      return <Skeleton className='mt-3 h-5 w-40 max-w-full' />
    }
    if (profilesQuery.isError && !isNotFound(profilesQuery.error)) {
      return (
        <p className='text-muted-foreground mt-3 text-sm'>
          {t('Unable to load profile insights')}
        </p>
      )
    }
    return (
      <div className='mt-3 flex min-w-0 flex-wrap gap-1.5'>
        <Badge variant='outline'>
          {t('{{count}} profile signals in 30 days', {
            count: totalProfiles,
          })}
        </Badge>
        {profiles.map((item) => (
          <Badge key={item.profile} variant='secondary'>
            {t(PROFILE_LABELS[item.profile] ?? 'Unknown profile')}: {item.count}
          </Badge>
        ))}
      </div>
    )
  }

  const renderFundingInsights = () => {
    if (fundingQuery.isLoading) {
      return (
        <div className='mt-3 grid min-w-0 grid-cols-2 gap-2 sm:grid-cols-4'>
          {[1, 2, 3, 4].map((key) => (
            <Skeleton key={key} className='h-14 w-full' />
          ))}
        </div>
      )
    }
    if (fundingQuery.isError && !isNotFound(fundingQuery.error)) {
      return (
        <p className='text-muted-foreground mt-3 text-sm'>
          {t('Unable to load AI usage and cost')}
        </p>
      )
    }
    if (!funding) {
      return (
        <p className='text-muted-foreground mt-3 text-sm'>
          {t('No recent usage')}
        </p>
      )
    }
    return (
      <dl className='mt-3 grid min-w-0 grid-cols-2 gap-2 sm:grid-cols-4'>
        <div className='bg-muted/20 min-w-0 rounded-md p-2.5'>
          <dt className='text-muted-foreground text-xs'>{t('Cost')}</dt>
          <dd className='mt-1 truncate text-sm font-semibold'>
            {formatPlatformCost(funding.cost_usd)}
          </dd>
        </div>
        <div className='bg-muted/20 min-w-0 rounded-md p-2.5'>
          <dt className='text-muted-foreground text-xs'>{t('Requests')}</dt>
          <dd className='mt-1 truncate text-sm font-semibold'>
            {numberFormatter.format(funding.requests)}
          </dd>
        </div>
        <div className='bg-muted/20 min-w-0 rounded-md p-2.5'>
          <dt className='text-muted-foreground text-xs'>{t('Total tokens')}</dt>
          <dd className='mt-1 truncate text-sm font-semibold'>
            {numberFormatter.format(funding.total_tokens)}
          </dd>
        </div>
        <div className='bg-muted/20 min-w-0 rounded-md p-2.5'>
          <dt className='text-muted-foreground text-xs'>
            {t('Remaining quota')}
          </dt>
          <dd className='mt-1 truncate text-sm font-semibold'>
            {formatPlatformCost(funding.remaining_usd)}
          </dd>
          <dd className='text-muted-foreground truncate text-xs'>
            {numberFormatter.format(funding.remaining_quota)}{' '}
            {t('Remaining quota units')}
          </dd>
        </div>
      </dl>
    )
  }

  const renderFirstQuestionInsights = () => {
    if (firstQuestionsQuery.isLoading) {
      return (
        <div className='mt-3 space-y-3' aria-hidden='true'>
          {[1, 2, 3].map((key) => (
            <Skeleton key={key} className='h-8 w-full' />
          ))}
        </div>
      )
    }
    if (firstQuestionsQuery.isError && !isNotFound(firstQuestionsQuery.error)) {
      return (
        <p className='text-muted-foreground mt-3 text-sm'>
          {t('Unable to load first-question insights')}
        </p>
      )
    }
    if (firstQuestions.length === 0) {
      return (
        <p className='text-muted-foreground mt-3 text-sm'>
          {t('No first-question data yet')}
        </p>
      )
    }
    return (
      <ol
        className='divide-border/70 mt-3 divide-y'
        data-testid='assistant-first-question-list'
      >
        {firstQuestions.map((item, index) => (
          <li
            key={`${item.question}-${item.last_asked_at}`}
            className='flex min-w-0 items-start gap-3 py-3 first:pt-0 last:pb-0'
          >
            <span className='text-muted-foreground w-5 shrink-0 pt-0.5 text-xs tabular-nums'>
              {index + 1}
            </span>
            <p className='min-w-0 flex-1 text-sm break-words'>
              {item.question}
            </p>
            <Badge variant='secondary' className='shrink-0 tabular-nums'>
              {numberFormatter.format(item.count)}
            </Badge>
          </li>
        ))}
      </ol>
    )
  }

  const renderInsights = () => (
    <div
      className='grid min-w-0 gap-3 sm:grid-cols-2'
      data-testid='assistant-operations-insights'
    >
      <section className='min-w-0 rounded-lg border p-3 sm:p-4'>
        <h3 className='text-sm font-medium'>{t('Intent signals')}</h3>
        <p className='text-muted-foreground mt-1 text-xs'>
          {t('Privacy-minimized assistant intent counts for the last 30 days.')}
        </p>
        {renderIntentInsights()}
      </section>

      <section className='min-w-0 rounded-lg border p-3 sm:p-4'>
        <h3 className='text-sm font-medium'>{t('Customer profiles')}</h3>
        <p className='text-muted-foreground mt-1 text-xs'>
          {t('Privacy-minimized profile signals for the last 30 days.')}
        </p>
        {renderProfileInsights()}
      </section>

      <section className='min-w-0 rounded-lg border p-3 sm:col-span-2 sm:p-4'>
        <h3 className='text-sm font-medium'>{t('Top first questions')}</h3>
        <p className='text-muted-foreground mt-1 text-xs'>
          {t(
            'Privacy-minimized real-user first questions counted from the first turn in the last 30 days.'
          )}
        </p>
        {renderFirstQuestionInsights()}
      </section>

      <section className='min-w-0 rounded-lg border p-3 sm:col-span-2 sm:p-4'>
        <div className='min-w-0'>
          <h3 className='text-sm font-medium'>{t('AI usage and cost')}</h3>
          <p className='text-muted-foreground mt-1 text-xs'>
            {t(
              'AI customer-service usage is charged to the super administrator account, not the user wallet.'
            )}
          </p>
        </div>
        {renderFundingInsights()}
      </section>
    </div>
  )

  return (
    <Card
      className='max-w-full min-w-0 overflow-hidden border-0 bg-transparent shadow-none'
      data-testid='assistant-support-tasks'
    >
      <CardHeader className='border-border gap-3 border-t px-0 pt-10'>
        <div className='min-w-0'>
          <CardTitle className='flex min-w-0 flex-wrap items-center gap-2'>
            <MessageSquareText className='size-4 shrink-0' aria-hidden='true' />
            <span className='break-words'>{t('Assistant support tasks')}</span>
            <Badge variant={pending.length > 0 ? 'default' : 'secondary'}>
              {pending.length} {t('pending')}
            </Badge>
          </CardTitle>
          <CardDescription className='mt-1 max-w-2xl'>
            {t('Turn explicit human-support requests into clear next actions.')}
          </CardDescription>
        </div>
        <CardAction className='shrink-0'>
          <Button
            data-testid='assistant-support-refresh'
            variant='outline'
            size='sm'
            aria-busy={isRefreshing}
            onClick={() => void refresh()}
            disabled={isRefreshing}
          >
            <RefreshCw
              data-icon='inline-start'
              className={isRefreshing ? 'animate-spin' : undefined}
              aria-hidden='true'
            />
            {isRefreshing ? t('Refreshing') : t('Refresh')}
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent className='min-w-0 space-y-8 overflow-hidden px-0'>
        {firstError ? (
          <Alert variant='destructive' role='alert'>
            <CircleAlert aria-hidden='true' />
            <AlertTitle>
              {t('Unable to load assistant support tasks')}
            </AlertTitle>
            <AlertDescription className='break-words'>
              {firstError instanceof Error
                ? firstError.message
                : t('Unable to load assistant support tasks')}
            </AlertDescription>
            <AlertAction>
              <Button
                variant='outline'
                size='sm'
                onClick={() => void refresh()}
                disabled={isRefreshing}
              >
                {t('Retry')}
              </Button>
            </AlertAction>
          </Alert>
        ) : null}

        <section
          aria-labelledby='assistant-support-task-list-title'
          className='min-w-0 space-y-3'
          data-testid='assistant-pending-workspace'
        >
          <div className='flex min-w-0 flex-wrap items-center justify-between gap-3 py-2'>
            <div className='min-w-0'>
              <p
                id='assistant-support-task-list-title'
                className='text-xs font-semibold tracking-wide uppercase'
              >
                {t('Pending work')}
              </p>
              <p className='mt-1 flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1'>
                <span
                  className='text-2xl leading-none font-semibold tabular-nums'
                  data-testid='assistant-pending-count'
                >
                  {pending.length}
                </span>{' '}
                <span className='text-muted-foreground text-sm'>
                  {t('support tasks waiting for review')}
                </span>
              </p>
            </div>
            <Badge
              variant={pending.length > 0 ? 'destructive' : 'secondary'}
              data-testid='assistant-pending-status'
            >
              {pending.length > 0 ? t('Action required') : t('All clear')}
            </Badge>
          </div>
          {renderPending()}
        </section>

        <section
          aria-label={t('Assistant support history and insights')}
          className='min-w-0 border-t pt-4'
          data-testid='assistant-secondary-workspace'
        >
          <Tabs defaultValue='insights' className='min-w-0'>
            <TabsList className='border-border flex h-auto w-full max-w-full flex-wrap justify-start gap-6 overflow-hidden rounded-none border-b bg-transparent p-0'>
              <TabsTrigger
                className='min-w-0 rounded-none border-0 bg-transparent px-0 py-3 text-center text-xs whitespace-normal shadow-none sm:flex-none sm:text-sm'
                value='resolved'
              >
                {t('Resolved history')}
                <Badge variant='secondary'>{resolved.length}</Badge>
              </TabsTrigger>
              <TabsTrigger
                className='min-w-0 rounded-none border-0 bg-transparent px-0 py-3 text-center text-xs whitespace-normal shadow-none sm:flex-none sm:text-sm'
                value='insights'
              >
                {t('Insights and AI cost')}
              </TabsTrigger>
            </TabsList>
            <TabsContent value='resolved' className='mt-3 min-w-0'>
              {renderResolved()}
            </TabsContent>
            <TabsContent value='insights' className='mt-3 min-w-0'>
              {renderInsights()}
            </TabsContent>
          </Tabs>
        </section>
      </CardContent>
    </Card>
  )
}
