/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'
import { ChevronLeft, ChevronRight, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { useBillingUSD } from '@/hooks/use-billing-usd'
import { formatTimestampToDate } from '@/lib/format'

import { getSystemGroups } from '../api'
import {
  MODERATION_CATEGORY_LABELS,
  type MODERATION_CATEGORIES,
} from './moderation-config'
import { getModerationStats, listModerationReviews } from './security-audit-api'
import type {
  ModerationReview,
  ModerationReviewFilters,
} from './security-audit-types'

const SOURCE_LABELS = {
  relay_input: 'API input',
  assistant_input: 'Assistant input',
  assistant_output: 'Assistant output',
} as const
const STATUS_LABELS = {
  pending: 'Pending',
  running: 'Running',
  completed: 'Completed',
  failed: 'Failed',
  cancelled: 'Cancelled',
} as const
const FEE_STATUS_LABELS: Record<string, string> = {
  none: 'No deduction',
  charged: 'Charged',
  partial: 'Partially charged',
  insufficient_balance: 'Insufficient balance',
}
const ALL = '__all__'
function useFeeAmount() {
  const { formatQuota } = useBillingUSD()
  return (quota: number) =>
    formatQuota(quota, {
      digitsLarge: 6,
      digitsSmall: 8,
      abbreviate: false,
    })
}

export function ModerationReviewRow({ review }: { review: ModerationReview }) {
  const { t } = useTranslation()
  const feeAmount = useFeeAmount()
  const completed = review.status === 'completed'
  return (
    <article
      className='border-border/60 space-y-2 border-b py-4 last:border-b-0'
      data-testid='moderation-review-row'
    >
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <span className='text-sm font-medium'>
          {t(SOURCE_LABELS[review.source])} · {review.review_model}
        </span>
        <Badge variant={completed && review.flagged ? 'warning' : 'outline'}>
          {completed
            ? review.flagged
              ? t('Flagged')
              : t('Clear')
            : t(STATUS_LABELS[review.status])}
        </Badge>
      </div>
      <p className='text-muted-foreground flex flex-wrap gap-x-3 gap-y-1 text-xs'>
        <span>{formatTimestampToDate(review.created_at)}</span>
        <span className='font-mono'>{review.group}</span>
        {review.user_id > 0 ? <span>#{review.user_id}</span> : null}
        <span>
          {t(
            review.mode === 'strict'
              ? 'Strict mode'
              : review.mode === 'tolerant'
                ? 'Tolerant mode'
                : 'Off'
          )}
        </span>
      </p>
      {review.categories.length ? (
        <p className='text-muted-foreground text-xs'>
          {review.categories
            .map((category) =>
              t(
                MODERATION_CATEGORY_LABELS[
                  category as (typeof MODERATION_CATEGORIES)[number]
                ] ?? category
              )
            )
            .join(', ')}
        </p>
      ) : null}
      <details className='text-muted-foreground text-xs leading-5'>
        <summary className='cursor-pointer'>{t('View details')}</summary>
        <dl className='mt-2 grid gap-x-3 gap-y-1 border-l pl-3 sm:grid-cols-[auto_minmax(0,1fr)]'>
          <dt>{t('Request ID')}</dt>
          <dd className='font-mono break-all'>{review.request_id}</dd>
          {review.subject_identifier ? (
            <>
              <dt>{t('Private user identifier')}</dt>
              <dd className='font-mono break-all'>
                {review.subject_identifier}
              </dd>
            </>
          ) : null}
          {review.provider_calls?.length ? (
            <>
              <dt>{t('Upstream moderation calls')}</dt>
              <dd className='min-w-0 space-y-2'>
                {review.provider_calls.map((call) => (
                  <div key={`${call.attempt}:${call.batch_index}`}>
                    <p>
                      {t('Attempt {{attempt}}, batch {{batch}}', {
                        attempt: call.attempt,
                        batch: call.batch_index,
                      })}
                    </p>
                    {call.response_id ? (
                      <p className='font-mono break-all'>
                        {t('Upstream response ID')}: {call.response_id}
                      </p>
                    ) : null}
                    {call.request_id ? (
                      <p className='font-mono break-all'>
                        {t('Upstream request ID')}: {call.request_id}
                      </p>
                    ) : null}
                  </div>
                ))}
              </dd>
            </>
          ) : null}
          <dt>{t('Review status')}</dt>
          <dd>{t(STATUS_LABELS[review.status])}</dd>
          <dt>{t('Requested category fee')}</dt>
          <dd className='tabular-nums'>{feeAmount(review.requested_quota)}</dd>
          <dt>{t('Wallet deduction')}</dt>
          <dd className='tabular-nums'>{feeAmount(review.charged_quota)}</dd>
          {review.fee_record_id > 0 ? (
            <>
              <dt>{t('Fee record ID')}</dt>
              <dd>#{review.fee_record_id}</dd>
            </>
          ) : null}
          {review.fee_status ? (
            <>
              <dt>{t('Fee status')}</dt>
              <dd>
                {t(FEE_STATUS_LABELS[review.fee_status] ?? review.fee_status)}
              </dd>
            </>
          ) : null}
          {review.input_truncated ? (
            <>
              <dt>{t('Input coverage')}</dt>
              <dd>{t('Input was truncated; no penalty applied.')}</dd>
            </>
          ) : null}
          {review.error ? (
            <>
              <dt>{t('Review error')}</dt>
              <dd className='break-words'>{review.error}</dd>
            </>
          ) : null}
        </dl>
      </details>
      {review.source === 'assistant_output' ? (
        <p className='text-muted-foreground text-xs'>
          {t('Model output is excluded from user penalties and risk scoring.')}
        </p>
      ) : null}
    </article>
  )
}

export function ModerationAuditPanel() {
  const { t } = useTranslation()
  const feeAmount = useFeeAmount()
  const [filters, setFilters] = useState<ModerationReviewFilters>({
    page: 1,
    page_size: 20,
  })
  const groups = useQuery({
    queryKey: ['groups'],
    queryFn: getSystemGroups,
    staleTime: 60_000,
  })
  const statsQuery = useQuery({
    queryKey: ['admin-moderation-stats'],
    queryFn: getModerationStats,
    retry: false,
    refetchInterval: 15_000,
  })
  const reviewsQuery = useQuery({
    queryKey: ['admin-moderation-reviews', filters],
    queryFn: () => listModerationReviews(filters),
    retry: false,
    refetchInterval: 15_000,
  })
  const stats = statsQuery.data?.success ? statsQuery.data.data : undefined
  const reviews = reviewsQuery.data?.success
    ? reviewsQuery.data.data
    : undefined
  const totalPages = Math.max(
    1,
    Math.ceil((reviews?.total ?? 0) / filters.page_size)
  )
  const filter = (key: 'group' | 'source' | 'status', value: string | null) =>
    setFilters((previous) => ({
      ...previous,
      page: 1,
      [key]: !value || value === ALL ? undefined : value,
    }))
  const selectors = [
    {
      key: 'group' as const,
      label: 'Group',
      options: (groups.data?.data ?? []).map((group) => ({
        value: group,
        label: group,
      })),
    },
    {
      key: 'source' as const,
      label: 'Review source',
      options: Object.entries(SOURCE_LABELS).map(([value, label]) => ({
        value,
        label: t(label),
      })),
    },
    {
      key: 'status' as const,
      label: 'Review status',
      options: Object.entries(STATUS_LABELS).map(([value, label]) => ({
        value,
        label: t(label),
      })),
    },
  ]
  return (
    <section
      className='space-y-5 border-t pt-6'
      aria-labelledby='moderation-audit-title'
    >
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <h3 id='moderation-audit-title' className='text-sm font-medium'>
          {t('OpenAI Moderation audit')}
        </h3>
        <Button
          type='button'
          variant='ghost'
          size='sm'
          onClick={() => {
            void statsQuery.refetch()
            void reviewsQuery.refetch()
          }}
          disabled={reviewsQuery.isFetching || statsQuery.isFetching}
        >
          <RefreshCw className='size-4' />
          {t('Refresh')}
        </Button>
      </div>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Asynchronous review results and wallet deductions. Request text and credentials are never shown here.'
        )}
      </p>
      {stats ? (
        <dl className='grid grid-cols-2 gap-3 sm:grid-cols-4'>
          {(['pending', 'running', 'completed', 'failed'] as const).map(
            (key) => (
              <div key={key}>
                <dt className='text-muted-foreground text-xs'>
                  {t(STATUS_LABELS[key])}
                </dt>
                <dd className='mt-1 text-xl tabular-nums'>
                  {stats[key].toLocaleString()}
                </dd>
              </div>
            )
          )}
          <div>
            <dt className='text-muted-foreground text-xs'>{t('Flagged')}</dt>
            <dd className='tabular-nums'>{stats.flagged.toLocaleString()}</dd>
          </div>
          <div>
            <dt className='text-muted-foreground text-xs'>
              {t('Fined reviews')}
            </dt>
            <dd className='tabular-nums'>{stats.fined.toLocaleString()}</dd>
          </div>
          <div>
            <dt className='text-muted-foreground text-xs'>
              {t('Wallet deduction')}
            </dt>
            <dd className='tabular-nums'>{feeAmount(stats.charged_quota)}</dd>
          </div>
        </dl>
      ) : statsQuery.isLoading ? (
        <Skeleton className='h-16 w-full' />
      ) : null}
      <div className='grid gap-3 sm:grid-cols-3'>
        {selectors.map((selector) => (
          <div key={selector.key} className='space-y-1.5'>
            <Label htmlFor={`moderation-filter-${selector.key}`}>
              {t(selector.label)}
            </Label>
            <Select
              value={filters[selector.key] ?? ALL}
              onValueChange={(value) => filter(selector.key, value)}
            >
              <SelectTrigger id={`moderation-filter-${selector.key}`}>
                <SelectValue>
                  {filters[selector.key]
                    ? (selector.options.find(
                        (option) => option.value === filters[selector.key]
                      )?.label ?? filters[selector.key])
                    : t('All')}
                </SelectValue>
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  <SelectItem value={ALL}>{t('All')}</SelectItem>
                  {selector.options.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>
        ))}
      </div>
      {reviewsQuery.isLoading ? (
        <Skeleton className='h-32 w-full' />
      ) : reviewsQuery.isError || !reviewsQuery.data?.success ? (
        <p className='text-destructive text-sm' role='alert'>
          {t('Unable to load moderation reviews. Try again.')}
        </p>
      ) : !reviews?.rows.length ? (
        <p className='text-muted-foreground text-sm'>
          {t('No moderation reviews match these filters.')}
        </p>
      ) : (
        <div>
          {reviews.rows.map((review) => (
            <ModerationReviewRow key={review.id} review={review} />
          ))}
        </div>
      )}
      {reviews && reviews.total > 0 ? (
        <div className='flex items-center justify-between gap-3 text-xs'>
          <span>
            {filters.page} / {totalPages} · {reviews.total.toLocaleString()}
          </span>
          <div className='flex gap-2'>
            <Button
              type='button'
              variant='outline'
              size='icon-sm'
              aria-label={t('Previous page')}
              disabled={filters.page <= 1 || reviewsQuery.isFetching}
              onClick={() =>
                setFilters((previous) => ({
                  ...previous,
                  page: Math.max(1, previous.page - 1),
                }))
              }
            >
              <ChevronLeft />
            </Button>
            <Button
              type='button'
              variant='outline'
              size='icon-sm'
              aria-label={t('Next page')}
              disabled={filters.page >= totalPages || reviewsQuery.isFetching}
              onClick={() =>
                setFilters((previous) => ({
                  ...previous,
                  page: Math.min(totalPages, previous.page + 1),
                }))
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
