/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { ChevronLeft, ChevronRight, Filter, ShieldCheck } from 'lucide-react'
import {
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useMemo,
  useState,
} from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { formatTimestampToDate } from '@/lib/format'

import { getAssistantReviewRun, listAssistantReviewRuns } from '../api'
import type { SystemTask } from '../types'
import {
  getAdminSecurityPolicy,
  getAdminSecurityStats,
  listAdminSecurityAIReviews,
  listAdminSecurityEvents,
} from './security-audit-api'
import type {
  AdminSecurityPolicy,
  AssistantReviewTask,
  SecurityAuditAIReview,
  SecurityAuditEvent,
  SecurityAuditFilters,
} from './security-audit-types'
import {
  securityAuditTotalPages,
  securityAuditUserFilter,
} from './security-audit-utils'

const PAGE_SIZE = 20
const AssistantReviewCleanup = lazy(() =>
  import('./assistant-review-cleanup').then((module) => ({
    default: module.AssistantReviewCleanup,
  }))
)

const ALL = '__all__'

type AuditFilterState = Pick<
  SecurityAuditFilters,
  'category' | 'group' | 'decision' | 'source'
>

function shortIdentifier(value: string | undefined): string {
  const trimmed = value?.trim()
  if (!trimmed) return '—'
  if (trimmed.length <= 16) return trimmed
  return `${trimmed.slice(0, 9)}…${trimmed.slice(-5)}`
}

function getProtectedGroups(policy: AdminSecurityPolicy | undefined): string[] {
  if (!policy || !policy.settings.enabled) return []
  return [
    ...new Set(
      policy.rules
        .filter((rule) => rule.enabled)
        .flatMap((rule) => rule.groups)
        .map((group) => group.trim())
        .filter(Boolean)
    ),
  ].sort((left, right) => left.localeCompare(right))
}

function sourceLabel(
  source: string | undefined,
  t: ReturnType<typeof useTranslation>['t']
) {
  switch (source) {
    case 'ai_review':
    case 'assistant_review':
      return t('assistant.security_review')
    case 'deterministic':
    case 'deterministic_rule':
    case 'advanced_security':
      return t('Deterministic rule')
    default:
      return source?.trim() || t('Not published')
  }
}

function decisionLabel(
  decision: string | undefined,
  t: ReturnType<typeof useTranslation>['t']
) {
  if (decision === 'blocked') return t('Blocked')
  if (decision === 'audited') return t('Audited matches')
  if (decision === 'violation') return t('Violation')
  if (decision === 'clear') return t('Clear')
  return decision?.trim() || t('Not published')
}

function aiReviewToEvent(review: SecurityAuditAIReview): SecurityAuditEvent {
  const isViolation = review.violation === true
  return {
    id: review.id,
    created_at: review.created_at,
    request_id: review.request_id,
    user_id: review.user_id,
    group: review.group,
    source: 'ai_review',
    decision: isViolation ? 'violation' : 'clear',
    category: 'assistant_review',
    rule_name: review.rules?.join(', '),
    review_model: review.review_model,
    status: review.status,
    violation: review.violation,
    abuse: review.abuse,
    rules: review.rules,
    explanation: review.explanation,
  }
}

function MetricStrip({
  stats,
  isLoading,
}: {
  stats?: {
    total_matches: number
    blocked_matches: number
    audited_matches: number
    affected_requests: number
    affected_users: number
    ai_review?: {
      total: number
      violations: number
      abuses: number
    }
  }
  isLoading: boolean
}) {
  const { t } = useTranslation()
  const metrics = [
    ['Total matches', stats?.total_matches],
    ['Blocked matches', stats?.blocked_matches],
    ['Audited matches', stats?.audited_matches],
    ['Affected requests', stats?.affected_requests],
    ['Affected users', stats?.affected_users],
  ] as const

  return (
    <div className='border-border/70 grid gap-4 border-y py-4 sm:grid-cols-5'>
      {metrics.map(([label, value]) => (
        <div key={label} className='min-w-0'>
          <p className='text-muted-foreground text-xs'>{t(label)}</p>
          {isLoading ? (
            <Skeleton className='mt-2 h-7 w-16' />
          ) : (
            <p className='mt-1 text-xl font-medium tabular-nums'>
              {(value ?? 0).toLocaleString()}
            </p>
          )}
        </div>
      ))}
      {stats?.ai_review ? (
        <p className='text-muted-foreground border-t pt-3 text-xs sm:col-span-5'>
          {t('assistant.security_review')} ·{' '}
          {stats.ai_review.total.toLocaleString()} {t('Reviews')} ·{' '}
          {stats.ai_review.violations.toLocaleString()} {t('Violation')} ·{' '}
          {stats.ai_review.abuses.toLocaleString()} {t('Abuse')}
        </p>
      ) : null}
    </div>
  )
}

function reviewCount(rows: Array<{ count: number }> | undefined): number {
  return (rows ?? []).reduce((total, row) => total + (row.count || 0), 0)
}

function reviewLabel(value: string): string {
  return value
    .replaceAll('_', ' ')
    .replaceAll(/\b\w/g, (letter) => letter.toUpperCase())
}

function ReviewBreakdown({
  title,
  rows,
  valueLabel,
  limit = 5,
}: {
  title: string
  rows: Array<{ label: string; count: number }>
  valueLabel?: string
  limit?: number
}) {
  const total = reviewCount(rows)
  return (
    <div className='min-w-0 space-y-2'>
      <div className='flex items-center justify-between gap-2'>
        <h5 className='text-xs font-medium'>{title}</h5>
        {valueLabel ? (
          <span className='text-muted-foreground text-[11px]'>
            {valueLabel}
          </span>
        ) : null}
      </div>
      {rows.length === 0 ? (
        <p className='text-muted-foreground text-xs'>—</p>
      ) : (
        <div className='space-y-1.5'>
          {rows.slice(0, limit).map((row) => (
            <div key={row.label} className='min-w-0'>
              <div className='flex items-center justify-between gap-2 text-xs'>
                <span className='min-w-0 truncate'>{row.label}</span>
                <span className='text-muted-foreground shrink-0 tabular-nums'>
                  {row.count.toLocaleString()}
                </span>
              </div>
              <div className='bg-muted mt-1 h-1 overflow-hidden rounded-full'>
                <div
                  className='bg-foreground/60 h-full rounded-full'
                  style={{
                    width: `${total > 0 ? (row.count / total) * 100 : 0}%`,
                  }}
                />
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function AssistantReviewSummary({
  task,
  isLoading,
}: {
  task?: AssistantReviewTask | null
  isLoading: boolean
}) {
  const { t } = useTranslation()

  if (isLoading) {
    return (
      <section className='border-border/70 space-y-4 border-y py-5'>
        <Skeleton className='h-5 w-48' />
        <div className='grid gap-3 sm:grid-cols-4'>
          {Array.from({ length: 4 }, (_, index) => (
            <Skeleton key={index} className='h-14' />
          ))}
        </div>
      </section>
    )
  }

  const review = task?.result
  if (!task || !review) {
    return (
      <section className='border-border/70 space-y-1 border-y py-5'>
        <h3 className='text-sm font-medium'>{t('Automatic review')}</h3>
        <p className='text-muted-foreground text-xs leading-5'>
          {task?.error || t('No completed assistant review is available yet.')}
        </p>
      </section>
    )
  }

  const hasDistilledIntents = (review.distilled_intents?.length ?? 0) > 0
  const reviewedIntents = hasDistilledIntents
    ? review.distilled_intents
    : review.intents
  const intentTitle = hasDistilledIntents
    ? t('Distilled intent themes')
    : t('Intent signals')
  const metrics = [
    [intentTitle, reviewCount(reviewedIntents)],
    [t('Profiles'), reviewCount(review.profiles)],
    [t('Pending support'), review.current_pending_support ?? 0],
    [t('Security incidents'), review.current_open_security_incidents ?? 0],
  ] as const
  const intentRows = (reviewedIntents ?? []).map((row) => ({
    label: reviewLabel(row.intent),
    count: row.count,
  }))
  const profileRows = (review.profiles ?? []).map((row) => ({
    label: reviewLabel(row.profile),
    count: row.count,
  }))
  const presetRows = review.presets ?? []
  const questionRows = (review.first_questions ?? []).map((row) => ({
    label: row.question,
    count: row.count,
  }))
  const actions = review.actions ?? []

  return (
    <section className='border-border/70 space-y-5 border-y py-5'>
      <div className='flex flex-wrap items-start justify-between gap-3'>
        <div>
          <h3 className='text-sm font-medium'>{t('Automatic review')}</h3>
          <p className='text-muted-foreground mt-1 text-xs leading-5'>
            {formatTimestampToDate(review.window_start)} —{' '}
            {formatTimestampToDate(review.window_end)} · {t(task.status)}
          </p>
        </div>
        <Badge variant={task.status === 'succeeded' ? 'outline' : 'secondary'}>
          {t(task.status)}
        </Badge>
      </div>

      <div className='grid gap-3 sm:grid-cols-4'>
        {metrics.map(([label, value]) => (
          <div key={label} className='min-w-0'>
            <p className='text-muted-foreground text-xs'>{label}</p>
            <p className='mt-1 text-lg font-medium tabular-nums'>
              {value.toLocaleString()}
            </p>
          </div>
        ))}
      </div>

      <div className='grid gap-6 lg:grid-cols-3'>
        <ReviewBreakdown
          title={intentTitle}
          rows={intentRows}
          valueLabel={
            hasDistilledIntents
              ? t('Distilled from Other and first-turn questions')
              : undefined
          }
        />
        <ReviewBreakdown title={t('Profiles')} rows={profileRows} />
        <div className='min-w-0 space-y-2'>
          <h5 className='text-xs font-medium'>{t('Chat Presets')}</h5>
          {presetRows.length === 0 ? (
            <p className='text-muted-foreground text-xs'>—</p>
          ) : (
            <div className='space-y-1.5'>
              {presetRows.slice(0, 5).map((row) => (
                <div
                  key={row.preset_id}
                  className='flex items-center justify-between gap-3 text-xs'
                >
                  <span className='min-w-0 truncate font-mono'>
                    {row.preset_id}
                  </span>
                  <span className='text-muted-foreground shrink-0 tabular-nums'>
                    {row.clicks.toLocaleString()} /{' '}
                    {row.conversations.toLocaleString()} /{' '}
                    {row.approvals.toLocaleString()}
                  </span>
                </div>
              ))}
              <p className='text-muted-foreground text-[10px]'>
                {t('Clicks / conversations / approvals')}
              </p>
            </div>
          )}
        </div>
      </div>

      <ReviewBreakdown
        title={t('Extracted first questions')}
        rows={questionRows}
        limit={10}
        valueLabel={t('Redacted and aggregated before display')}
      />

      {review.commerce || review.security ? (
        <div className='grid gap-3 border-t pt-4 sm:grid-cols-2'>
          {review.commerce ? (
            <div className='space-y-1 text-xs'>
              <h5 className='font-medium'>{t('Commerce')}</h5>
              <p className='text-muted-foreground leading-5'>
                {t('Chat users')}: {review.commerce.chat_users.toLocaleString()}{' '}
                · {t('Paid users')}:{' '}
                {review.commerce.paid_users.toLocaleString()} ·{' '}
                {t('Conversion rate')}:{' '}
                {review.commerce.conversion_rate_percent}% · {t('Refunds')}:{' '}
                {review.commerce.refund_count.toLocaleString()}
              </p>
            </div>
          ) : null}
          {review.security ? (
            <div className='space-y-1 text-xs'>
              <h5 className='font-medium'>{t('Security audit')}</h5>
              <p className='text-muted-foreground leading-5'>
                {t('Matches')}: {review.security.total_matches.toLocaleString()}{' '}
                · {t('Blocked')}:{' '}
                {review.security.blocked_matches.toLocaleString()} ·{' '}
                {t('Affected users')}:{' '}
                {review.security.affected_users.toLocaleString()}
              </p>
            </div>
          ) : null}
        </div>
      ) : (
        <p className='text-muted-foreground border-t pt-4 text-xs leading-5'>
          {t(
            'This run contains aggregate assistant metrics only. Detailed security and commerce sections will appear after the backend update.'
          )}
        </p>
      )}

      {actions.length > 0 ? (
        <div className='space-y-2 border-t pt-4'>
          <h5 className='text-xs font-medium'>{t('Actions')}</h5>
          <div className='flex flex-wrap gap-2'>
            {actions.map((action) => (
              <Badge key={action.code} variant='outline'>
                {reviewLabel(action.code)} · {action.count.toLocaleString()}
              </Badge>
            ))}
          </div>
        </div>
      ) : null}
    </section>
  )
}

function AssistantReviewHistory({
  tasks,
  selectedTaskId,
  onSelect,
  onCleaned,
  isLoading,
}: {
  tasks: SystemTask[]
  selectedTaskId?: string
  onSelect: (taskId: string) => void
  onCleaned: () => void
  isLoading: boolean
}) {
  const { t } = useTranslation()
  const reviewTasks = tasks.filter((task) => task.type === 'assistant_review')

  return (
    <section className='border-border/70 space-y-3 border-y py-4'>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <h3 className='text-sm font-medium'>
          {t('Automatic review')} · {t('System task records')}
        </h3>
        <div className='flex items-center gap-2'>
          <Suspense fallback={<Skeleton className='h-8 w-36' />}>
            <AssistantReviewCleanup
              disabled={isLoading}
              onCleaned={onCleaned}
            />
          </Suspense>
          <span className='text-muted-foreground text-xs tabular-nums'>
            {reviewTasks.length.toLocaleString()}
          </span>
        </div>
      </div>
      {isLoading ? (
        <div className='space-y-2'>
          {Array.from({ length: 3 }, (_, index) => (
            <Skeleton key={index} className='h-10 w-full' />
          ))}
        </div>
      ) : reviewTasks.length === 0 ? (
        <p className='text-muted-foreground text-xs leading-5'>
          {t('No completed assistant review is available yet.')}
        </p>
      ) : (
        <div className='divide-border/70 divide-y'>
          {reviewTasks.map((task) => {
            const selected = task.task_id === selectedTaskId
            return (
              <button
                key={task.task_id}
                type='button'
                className='hover:bg-muted/40 focus-visible:ring-ring flex w-full items-center justify-between gap-3 py-3 text-left transition-colors outline-none focus-visible:ring-2 focus-visible:ring-offset-2'
                aria-pressed={selected}
                onClick={() => onSelect(task.task_id)}
              >
                <span className='min-w-0'>
                  <span className='flex items-center gap-2 text-xs font-medium'>
                    <span
                      className={
                        selected
                          ? 'bg-foreground size-1.5 rounded-full'
                          : 'bg-muted-foreground/40 size-1.5 rounded-full'
                      }
                      aria-hidden='true'
                    />
                    {t(task.status)}
                  </span>
                  <span className='text-muted-foreground mt-1 block truncate text-[11px]'>
                    {formatTimestampToDate(task.updated_at)} ·{' '}
                    {shortIdentifier(task.task_id)}
                  </span>
                </span>
                <span className='text-muted-foreground shrink-0 text-xs'>
                  {selected ? t('Current') : t('View')}
                </span>
              </button>
            )
          })}
        </div>
      )}
    </section>
  )
}

function ProtectedGroups({ policy }: { policy?: AdminSecurityPolicy }) {
  const { t } = useTranslation()
  const groups = getProtectedGroups(policy)

  return (
    <section className='space-y-3' aria-labelledby='protected-groups-title'>
      <div className='flex items-start gap-2'>
        <ShieldCheck className='text-muted-foreground mt-0.5 size-4 shrink-0' />
        <div>
          <h4 id='protected-groups-title' className='text-sm font-medium'>
            {t('Protected groups')}
          </h4>
          <p className='text-muted-foreground mt-1 text-xs leading-5'>
            {t(
              'Only groups listed by an enabled rule are included. Rules do not apply globally.'
            )}
          </p>
        </div>
      </div>
      {groups.length > 0 ? (
        <div className='flex flex-wrap gap-2'>
          {groups.map((group) => (
            <Badge key={group} variant='outline' className='font-mono text-xs'>
              {group}
            </Badge>
          ))}
        </div>
      ) : (
        <p className='text-muted-foreground text-sm'>
          {t(
            'No groups are currently covered by enabled advanced security rules.'
          )}
        </p>
      )}
    </section>
  )
}

function FilterSelect({
  value,
  onChange,
  label,
  options,
}: {
  value: string
  onChange: (value: string) => void
  label: string
  options: string[]
}) {
  return (
    <Select
      value={value || ALL}
      onValueChange={(nextValue) => onChange(nextValue ?? ALL)}
    >
      <SelectTrigger aria-label={label} className='w-full sm:w-44'>
        <SelectValue placeholder={label} />
      </SelectTrigger>
      <SelectContent alignItemWithTrigger={false}>
        <SelectGroup>
          <SelectItem value={ALL}>{label}</SelectItem>
          {options.map((option) => (
            <SelectItem key={option} value={option}>
              {option}
            </SelectItem>
          ))}
        </SelectGroup>
      </SelectContent>
    </Select>
  )
}

export function AuditRow({ event }: { event: SecurityAuditEvent }) {
  const { t } = useTranslation()
  const userFilter = securityAuditUserFilter(event)
  const isAiReview =
    event.source === 'ai_review' || event.source === 'assistant_review'
  const hasDetails = Boolean(
    event.explanation ||
    event.request_id ||
    event.rule_id ||
    event.rule_version ||
    event.endpoint ||
    event.review_model
  )
  const title =
    isAiReview && event.review_model
      ? `${sourceLabel(event.source, t)} · ${event.review_model}`
      : sourceLabel(event.source, t)

  return (
    <div className='border-border/60 grid gap-2 border-b py-3 text-sm last:border-b-0 sm:grid-cols-[7.5rem_minmax(10rem,1.25fr)_minmax(8rem,1fr)_minmax(8rem,1fr)_auto] sm:items-start sm:gap-4'>
      <div className='text-muted-foreground text-xs leading-5'>
        {formatTimestampToDate(event.created_at)}
      </div>
      <div className='min-w-0'>
        <div className='flex flex-wrap items-center gap-2'>
          <span className='font-medium'>{title}</span>
          {event.severity ? (
            <Badge variant='outline' className='text-[10px]'>
              {event.severity}
            </Badge>
          ) : null}
        </div>
        <p className='text-muted-foreground mt-1 truncate font-mono text-xs'>
          {event.rule_name || event.rule_id || event.category || t('Details')}
        </p>
        {event.explanation ? (
          <p className='text-muted-foreground mt-1 line-clamp-2 text-xs leading-5'>
            {event.explanation}
          </p>
        ) : null}
        {userFilter ? (
          <Link
            to='/users'
            search={{
              page: 1,
              pageSize: undefined,
              filter: userFilter,
              status: [],
              role: [],
              group: '',
              l0Only: false,
            }}
            className='text-muted-foreground hover:text-foreground mt-1 inline-flex max-w-full truncate text-xs underline-offset-4 hover:underline'
            title={t('View user in user management')}
          >
            {event.username?.trim()
              ? `@${event.username.trim()}`
              : `#${event.user_id}`}
            {event.username?.trim() && event.user_id
              ? ` · #${event.user_id}`
              : ''}
          </Link>
        ) : null}
        {hasDetails ? (
          <details className='text-muted-foreground mt-2 text-xs leading-5'>
            <summary className='hover:text-foreground cursor-pointer underline-offset-4 hover:underline'>
              {t('View details')}
            </summary>
            <dl className='border-border/60 mt-2 grid gap-x-3 gap-y-1 border-l pl-3 sm:grid-cols-[auto_minmax(0,1fr)]'>
              {event.explanation ? (
                <>
                  <dt>{t('Explanation')}</dt>
                  <dd className='text-foreground break-words'>
                    {event.explanation}
                  </dd>
                </>
              ) : null}
              {event.request_id ? (
                <>
                  <dt>{t('Request ID')}</dt>
                  <dd className='text-foreground font-mono break-all'>
                    {event.request_id}
                  </dd>
                </>
              ) : null}
              {event.rule_id ? (
                <>
                  <dt>{t('Rule')}</dt>
                  <dd className='text-foreground font-mono break-all'>
                    {event.rule_id}
                    {event.rule_version ? ` · ${event.rule_version}` : ''}
                  </dd>
                </>
              ) : null}
              {event.endpoint ? (
                <>
                  <dt>{t('Endpoint')}</dt>
                  <dd className='text-foreground font-mono break-all'>
                    {event.endpoint}
                  </dd>
                </>
              ) : null}
              {event.review_model ? (
                <>
                  <dt>{t('Review model')}</dt>
                  <dd className='text-foreground font-mono break-all'>
                    {event.review_model}
                  </dd>
                </>
              ) : null}
            </dl>
          </details>
        ) : null}
      </div>
      <div className='min-w-0'>
        <p className='text-muted-foreground text-xs'>{t('Category')}</p>
        <p className='mt-1 truncate font-mono text-xs'>
          {event.category || '—'}
        </p>
      </div>
      <div className='min-w-0'>
        <p className='text-muted-foreground text-xs'>{t('Group')}</p>
        <p className='mt-1 truncate font-mono text-xs'>
          {event.group || '—'}
          {event.model_name ? ` · ${event.model_name}` : ''}
        </p>
      </div>
      <div className='flex items-center justify-between gap-3 sm:block sm:text-right'>
        <Badge
          variant={
            event.decision === 'blocked' || event.decision === 'violation'
              ? 'destructive'
              : 'outline'
          }
        >
          {decisionLabel(event.decision, t)}
        </Badge>
        <p className='text-muted-foreground mt-1 font-mono text-[10px]'>
          {shortIdentifier(event.request_id)}
        </p>
      </div>
    </div>
  )
}

export function SecurityAuditPanel() {
  const { t } = useTranslation()
  const [filters, setFilters] = useState<AuditFilterState>({})
  const [page, setPage] = useState(1)
  const [selectedReviewTaskId, setSelectedReviewTaskId] = useState<
    string | undefined
  >()

  const policyQuery = useQuery({
    queryKey: ['admin-security-policy'],
    queryFn: getAdminSecurityPolicy,
    retry: false,
    refetchOnWindowFocus: false,
    staleTime: 60_000,
  })
  const policy = policyQuery.data?.success ? policyQuery.data.data : undefined
  const reviewHistoryQuery = useQuery({
    queryKey: ['admin-assistant-review-history'],
    queryFn: () => listAssistantReviewRuns(30),
    retry: false,
    refetchOnWindowFocus: false,
    staleTime: 15_000,
  })
  const reviewTasks = useMemo(
    () =>
      (reviewHistoryQuery.data?.success
        ? (reviewHistoryQuery.data.data ?? [])
        : []
      ).filter((task) => task.type === 'assistant_review'),
    [reviewHistoryQuery.data]
  )
  useEffect(() => {
    if (reviewTasks.length === 0) {
      setSelectedReviewTaskId(undefined)
      return
    }
    if (
      !selectedReviewTaskId ||
      !reviewTasks.some((task) => task.task_id === selectedReviewTaskId)
    ) {
      setSelectedReviewTaskId(reviewTasks[0].task_id)
    }
  }, [reviewTasks, selectedReviewTaskId])
  const handleReviewHistoryCleaned = useCallback(() => {
    setSelectedReviewTaskId(undefined)
  }, [])
  const selectedReviewQuery = useQuery({
    queryKey: ['admin-assistant-review-task', selectedReviewTaskId],
    queryFn: () => {
      if (!selectedReviewTaskId) {
        throw new Error(
          'A review task must be selected before loading details.'
        )
      }
      return getAssistantReviewRun<AssistantReviewTask>(selectedReviewTaskId)
    },
    enabled: Boolean(selectedReviewTaskId),
    retry: false,
    refetchOnWindowFocus: false,
    staleTime: 15_000,
  })
  const selectedReviewTask = selectedReviewQuery.data?.success
    ? selectedReviewQuery.data.data
    : undefined
  const protectedGroups = useMemo(() => getProtectedGroups(policy), [policy])
  const categories = useMemo(
    () =>
      [
        ...new Set(
          (policy?.rules ?? [])
            .map((rule) => rule.category.trim())
            .filter(Boolean)
        ),
      ]
        .concat('assistant_review')
        .filter((value, index, values) => values.indexOf(value) === index)
        .sort((left, right) => left.localeCompare(right)),
    [policy]
  )
  const sources = useMemo(
    () =>
      [
        ...new Set([
          ...(policy?.rules ?? [])
            .map((rule) => rule.source.trim())
            .filter(Boolean),
          'ai_review',
        ]),
      ].sort((left, right) => left.localeCompare(right)),
    [policy]
  )

  const eventQuery = useQuery({
    queryKey: ['admin-security-events', filters, page],
    queryFn: () =>
      listAdminSecurityEvents({
        ...filters,
        page,
        page_size: PAGE_SIZE,
      }),
    enabled: !filters.source || filters.source !== 'ai_review',
    retry: false,
    refetchOnWindowFocus: false,
    staleTime: 15_000,
  })
  const aiReviewQuery = useQuery({
    queryKey: ['admin-security-ai-reviews', filters, page],
    queryFn: () =>
      listAdminSecurityAIReviews({
        ...filters,
        page,
        page_size: PAGE_SIZE,
      }),
    enabled: !filters.source || filters.source === 'ai_review',
    retry: false,
    refetchOnWindowFocus: false,
    staleTime: 15_000,
  })
  const statsQuery = useQuery({
    queryKey: ['admin-security-stats', filters],
    queryFn: () => getAdminSecurityStats(filters),
    retry: false,
    refetchOnWindowFocus: false,
    staleTime: 15_000,
  })

  const stats = statsQuery.data?.success ? statsQuery.data.data : undefined
  const pageData = eventQuery.data?.success ? eventQuery.data.data : undefined
  const aiPageData = aiReviewQuery.data?.success
    ? aiReviewQuery.data.data
    : undefined
  const aiRecordsUnavailable =
    filters.source === 'ai_review' && aiPageData?.available === false
  const events = useMemo(() => {
    const deterministic = pageData?.items ?? []
    const aiReviews = (aiPageData?.items ?? []).map(aiReviewToEvent)
    return [...deterministic, ...aiReviews].sort(
      (left, right) => right.created_at - left.created_at
    )
  }, [aiPageData?.items, pageData?.items])
  const totalItems = (pageData?.total ?? 0) + (aiPageData?.total ?? 0)
  const totalPages = securityAuditTotalPages({
    source: filters.source,
    deterministicTotal: pageData?.total ?? 0,
    aiReviewTotal: aiPageData?.total ?? 0,
    pageSize: PAGE_SIZE,
  })
  const setFilter = (key: keyof AuditFilterState, value: string) => {
    setPage(1)
    setFilters((previous) => ({
      ...previous,
      [key]: value === ALL ? undefined : value,
    }))
  }
  const clearFilters = () => {
    setPage(1)
    setFilters({})
  }

  if (
    policyQuery.isError &&
    (eventQuery.isError || filters.source === 'ai_review') &&
    (aiReviewQuery.isError || filters.source === 'deterministic_rule') &&
    statsQuery.isError
  ) {
    return (
      <section className='border-border/70 space-y-2 border-t pt-6'>
        <h3 className='text-sm font-medium'>{t('Security audit details')}</h3>
        <p className='text-muted-foreground text-sm'>
          {t('Audit data is available to administrators only.')}
        </p>
      </section>
    )
  }

  return (
    <section
      className='border-border/70 space-y-6 border-t pt-6'
      aria-labelledby='security-audit-title'
    >
      <div className='flex flex-wrap items-start justify-between gap-3'>
        <div>
          <h3 id='security-audit-title' className='text-sm font-medium'>
            {t('Security audit details')}
          </h3>
          <p className='text-muted-foreground mt-1 max-w-3xl text-xs leading-5'>
            {t(
              'Review results from deterministic rules and asynchronous AI audits. Prompt text, previews, matcher patterns, and credentials are never shown here.'
            )}
          </p>
        </div>
        <Badge variant={policy?.settings.enabled ? 'default' : 'outline'}>
          {policy?.settings.enabled ? t('Enabled') : t('Disabled')}
        </Badge>
      </div>

      <MetricStrip stats={stats} isLoading={statsQuery.isLoading} />
      <AssistantReviewSummary
        task={selectedReviewTask}
        isLoading={
          reviewHistoryQuery.isLoading || selectedReviewQuery.isLoading
        }
      />
      <AssistantReviewHistory
        tasks={reviewTasks}
        selectedTaskId={selectedReviewTaskId}
        onSelect={setSelectedReviewTaskId}
        onCleaned={handleReviewHistoryCleaned}
        isLoading={reviewHistoryQuery.isLoading}
      />
      <ProtectedGroups policy={policy} />

      <div className='space-y-3'>
        <div className='flex items-center gap-2'>
          <Filter className='text-muted-foreground size-4' />
          <span className='text-sm font-medium'>{t('Filters')}</span>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            className='ml-auto'
            onClick={clearFilters}
            disabled={Object.values(filters).every((value) => !value)}
          >
            {t('Clear filters')}
          </Button>
        </div>
        <div className='grid gap-2 sm:grid-cols-2 lg:grid-cols-4'>
          <FilterSelect
            label={t('All categories')}
            value={filters.category ?? ''}
            onChange={(value) => setFilter('category', value)}
            options={categories}
          />
          <FilterSelect
            label={t('All groups')}
            value={filters.group ?? ''}
            onChange={(value) => setFilter('group', value)}
            options={protectedGroups}
          />
          <FilterSelect
            label={t('All decisions')}
            value={filters.decision ?? ''}
            onChange={(value) => setFilter('decision', value)}
            options={['blocked', 'audited', 'violation', 'clear']}
          />
          <FilterSelect
            label={t('All sources')}
            value={filters.source ?? ''}
            onChange={(value) => setFilter('source', value)}
            options={sources}
          />
        </div>
      </div>

      <div className='space-y-1' aria-live='polite'>
        <div className='text-muted-foreground hidden grid-cols-[7.5rem_minmax(10rem,1.25fr)_minmax(8rem,1fr)_minmax(8rem,1fr)_auto] gap-4 px-0 text-xs sm:grid'>
          <span>{t('Occurred')}</span>
          <span>{t('Review source')}</span>
          <span>{t('Category')}</span>
          <span>{t('Group')}</span>
          <span className='text-right'>{t('Decision')}</span>
        </div>
        {eventQuery.isLoading || aiReviewQuery.isLoading ? (
          <div className='space-y-3 py-3'>
            {Array.from({ length: 4 }, (_, index) => (
              <Skeleton key={index} className='h-16 w-full' />
            ))}
          </div>
        ) : events.length > 0 ? (
          events.map((event) => (
            <AuditRow
              key={`${event.source ?? 'unknown'}:${event.id}`}
              event={event}
            />
          ))
        ) : (
          <p className='text-muted-foreground border-border/60 border-y py-8 text-center text-sm'>
            {aiRecordsUnavailable
              ? t(
                  'This run contains aggregate assistant metrics only. Detailed security and commerce sections will appear after the backend update.'
                )
              : t('No security audit events match the current filters.')}
          </p>
        )}
      </div>

      {totalItems > 0 ? (
        <div className='flex items-center justify-between gap-3'>
          <span className='text-muted-foreground text-xs tabular-nums'>
            {t('Page {{current}} of {{total}}', {
              current: page,
              total: totalPages,
            })}
          </span>
          <div className='flex items-center gap-1'>
            <Button
              type='button'
              variant='outline'
              size='icon-sm'
              aria-label={t('Previous page')}
              onClick={() => setPage((current) => Math.max(1, current - 1))}
              disabled={
                page <= 1 || eventQuery.isFetching || aiReviewQuery.isFetching
              }
            >
              <ChevronLeft />
            </Button>
            <Button
              type='button'
              variant='outline'
              size='icon-sm'
              aria-label={t('Next page')}
              onClick={() =>
                setPage((current) => Math.min(totalPages, current + 1))
              }
              disabled={
                page >= totalPages ||
                eventQuery.isFetching ||
                aiReviewQuery.isFetching
              }
            >
              <ChevronRight />
            </Button>
          </div>
        </div>
      ) : null}
    </section>
  )
}
