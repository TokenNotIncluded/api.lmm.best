/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useTranslation } from 'react-i18next'

import { MODERATION_CATEGORY_LABELS } from '@/features/system-settings/security/moderation-config'

import { MARKET_AI_REVIEW_COPY as copy } from './copy'

/** View data only. The API adapter must use persisted server results. */
export interface MarketAIReviewResult {
  status: string
  decision?: string
  applied: boolean
  categories: string[]
  categoriesKnown?: boolean
  mode?: string
  outcome?: string | null
  error?: string
  checkedAt?: number
}

export function MarketAIReviewResultView({
  result,
}: {
  result?: MarketAIReviewResult | null
}) {
  const { t } = useTranslation()
  if (!result) return null
  const statuses: Record<string, string> = {
    queued: copy.queued,
    running: copy.running,
    succeeded: copy.succeeded,
    failed: copy.failed,
    cancelled: copy.cancelled,
    stale: copy.stale,
  }
  const decision =
    result.decision === 'approve'
      ? copy.approve
      : result.decision === 'reject'
        ? copy.reject
        : copy.pending
  return (
    <section
      aria-label={t(copy.result)}
      className='bg-muted space-y-2 rounded-md px-3 py-3 text-sm'
    >
      <h3 className='font-medium'>{t(copy.result)}</h3>
      <p>
        {t(
          Object.hasOwn(statuses, result.status)
            ? statuses[result.status]
            : copy.unknown
        )}
      </p>
      <p>
        {t(decision)} · {t(result.applied ? copy.applied : copy.notApplied)}
      </p>
      {result.outcome === 'overridden' ? <p>{t(copy.overridden)}</p> : null}
      {result.mode === 'assist' && result.outcome === 'reference' ? (
        <p>{t(copy.manual)}</p>
      ) : null}
      {result.categories.length > 0 ? (
        <p className='break-words'>
          {t(copy.categories)}:{' '}
          {result.categories
            .map((category) =>
              Object.hasOwn(MODERATION_CATEGORY_LABELS, category)
                ? t(
                    MODERATION_CATEGORY_LABELS[
                      category as keyof typeof MODERATION_CATEGORY_LABELS
                    ]
                  )
                : category
            )
            .join(', ')}
        </p>
      ) : result.status === 'succeeded' && result.categoriesKnown !== false ? (
        <p>{t(copy.noCategories)}</p>
      ) : null}
      {result.error && (
        <p className='text-destructive break-words'>
          {t(copy.error)}: {t(result.error)}
        </p>
      )}
      {result.checkedAt && result.checkedAt > 0 ? (
        <p className='text-muted-foreground text-xs'>
          {t(copy.checkedAt)}:{' '}
          {new Date(result.checkedAt * 1000).toLocaleString()}
        </p>
      ) : null}
      <p className='text-muted-foreground text-xs'>{t(copy.limits)}</p>
      <p className='text-muted-foreground text-xs'>{t(copy.coverage)}</p>
    </section>
  )
}
