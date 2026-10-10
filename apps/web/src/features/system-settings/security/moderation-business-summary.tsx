/*
Copyright (C) 2026 LIghtJUNction
*/
import { useTranslation } from 'react-i18next'

import {
  recordedCount,
  recordedCredits,
  summarizePageAppeals,
} from './moderation-recorded-values'
import type {
  ModerationAppeal,
  ModerationQueueStats,
  ModerationReview,
} from './security-audit-types'

export function ModerationBusinessSummary({
  stats,
  reviews,
  appeals,
}: {
  stats?: ModerationQueueStats
  reviews?: ModerationReview[]
  appeals?: ModerationAppeal[]
}) {
  const { t, i18n } = useTranslation()
  const locale = i18n.resolvedLanguage || i18n.language
  const counts =
    Array.isArray(reviews) && Array.isArray(appeals)
      ? summarizePageAppeals(reviews, appeals)
      : undefined
  const metrics = [
    ['Pending', stats?.pending],
    ['Running', stats?.running],
    ['Completed', stats?.completed],
    ['Failed', stats?.failed],
    ['Cancelled', stats?.cancelled],
    ['Flagged', stats?.flagged],
    ['Fined reviews', stats?.fined],
  ] as const
  const unavailable = t('No data provided')
  return (
    <div className='space-y-5' data-testid='moderation-business-summary'>
      <section className='space-y-3' aria-labelledby='review-activity-title'>
        <div>
          <h4 id='review-activity-title' className='text-sm font-medium'>
            {t('Review activity')}
          </h4>
          <p className='text-muted-foreground mt-1 text-xs leading-5'>
            {t(
              'All-time review totals. Filters below apply only to the review list.'
            )}
          </p>
        </div>
        <dl className='grid grid-cols-2 gap-x-5 gap-y-4 border-y py-4 sm:grid-cols-4'>
          {metrics.map(([label, value]) => (
            <div key={label} className='min-w-0'>
              <dt className='text-muted-foreground text-xs'>{t(label)}</dt>
              <dd className='mt-1 text-lg font-medium tabular-nums'>
                {recordedCount(value, locale) ?? unavailable}
              </dd>
            </div>
          ))}
          <div className='min-w-0'>
            <dt className='text-muted-foreground text-xs'>
              {t('Recorded deductions')}
            </dt>
            <dd className='mt-1 text-lg font-medium break-words tabular-nums'>
              {recordedCredits(stats?.charged_quota, locale) ?? unavailable}
            </dd>
          </div>
        </dl>
        <p className='text-muted-foreground text-xs leading-5'>
          {t(
            'Original review credits include reversed or refunded records and are not net wallet spending.'
          )}
        </p>
      </section>
      <section
        className='space-y-3'
        aria-labelledby='moderation-page-appeals-title'
      >
        <div>
          <h4
            id='moderation-page-appeals-title'
            className='text-sm font-medium'
          >
            {t('Appeals linked to this page')}
          </h4>
          <p className='text-muted-foreground mt-1 text-xs leading-5'>
            {t(
              'Counts include only appeals from the latest 200 records linked to reviews on this page.'
            )}
          </p>
        </div>
        <dl className='grid grid-cols-3 gap-3'>
          {(
            [
              ['Pending', counts?.pending],
              ['Approved', counts?.approved],
              ['Rejected', counts?.rejected],
            ] as const
          ).map(([label, value]) => (
            <div key={label}>
              <dt className='text-muted-foreground text-xs'>{t(label)}</dt>
              <dd className='mt-1 text-lg font-medium tabular-nums'>
                {recordedCount(value, locale) ?? unavailable}
              </dd>
            </div>
          ))}
        </dl>
      </section>
    </div>
  )
}
