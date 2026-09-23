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
/*
Copyright (C) 2026 LIghtJUNction
*/
import {
  Alert02Icon,
  CheckmarkCircle01Icon,
  Clock01Icon,
  GitPullRequestIcon,
  LinkSquare02Icon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import type { ComponentProps } from 'react'
import { useTranslation } from 'react-i18next'

import { toIntlLocale } from '@/i18n/languages'
import { cn } from '@/lib/utils'

import { bountyTimeline } from './timeline'
import type { BountyChallenge } from './types'

type IconType = ComponentProps<typeof HugeiconsIcon>['icon']

/**
 * Tone per timeline milestone. Rejection-family events stay visible even when
 * a later dispute overturns them, so they are coloured, not hidden.
 */
const EVENT_TONE: Record<
  string,
  { icon: IconType; className: string; iconClassName: string }
> = {
  Accepted: {
    icon: CheckmarkCircle01Icon,
    className: 'border-primary/40 bg-primary/10 text-primary',
    iconClassName: 'text-primary',
  },
  Submitted: {
    icon: Clock01Icon,
    className: 'border-warning/40 bg-warning/10 text-warning',
    iconClassName: 'text-warning',
  },
  Rejected: {
    icon: Alert02Icon,
    className: 'border-destructive/40 bg-destructive/10 text-destructive',
    iconClassName: 'text-destructive',
  },
  Approved: {
    icon: CheckmarkCircle01Icon,
    className: 'border-success/40 bg-success/10 text-success',
    iconClassName: 'text-success',
  },
  'Dispute opened': {
    icon: Alert02Icon,
    className: 'border-warning/40 bg-warning/10 text-warning',
    iconClassName: 'text-warning',
  },
  'Resolved and paid': {
    icon: CheckmarkCircle01Icon,
    className: 'border-success/40 bg-success/10 text-success',
    iconClassName: 'text-success',
  },
  'Resolved and denied': {
    icon: Alert02Icon,
    className: 'border-destructive/40 bg-destructive/10 text-destructive',
    iconClassName: 'text-destructive',
  },
  'Reward credited to API balance': {
    icon: CheckmarkCircle01Icon,
    className: 'border-success/40 bg-success/10 text-success',
    iconClassName: 'text-success',
  },
}

const NEUTRAL_TONE = {
  icon: Clock01Icon,
  className: 'border-border bg-muted/40 text-foreground',
  iconClassName: 'text-muted-foreground',
}

export function BountyProgress({ challenge }: { challenge: BountyChallenge }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const events = bountyTimeline(challenge)
  const lastEventKey = events.at(-1)?.key

  return (
    <div className='my-4 text-sm'>
      <h3 className='mb-3 font-semibold'>{t('Delivery timeline')}</h3>
      <ol className='border-border space-y-4 border-l pl-4'>
        {events.map((event) => {
          const tone = EVENT_TONE[event.key] ?? NEUTRAL_TONE
          const isLatest = event.key === lastEventKey
          return (
            <li key={event.key} className='relative'>
              <span
                aria-hidden='true'
                className={cn(
                  'absolute top-0.5 -left-[1.3125rem] flex size-5 items-center justify-center rounded-full border bg-background',
                  tone.className
                )}
              >
                <HugeiconsIcon
                  icon={tone.icon}
                  strokeWidth={2.2}
                  aria-hidden='true'
                  className={cn('size-3', tone.iconClassName)}
                />
              </span>
              <div
                className={cn(
                  'flex flex-wrap items-center gap-x-2 gap-y-1',
                  isLatest ? 'font-medium' : undefined
                )}
              >
                <span>{t(event.key)}</span>
                {isLatest ? (
                  <span className='border-border bg-muted/40 text-muted-foreground rounded-full border px-1.5 py-0.5 text-[10px] font-medium'>
                    {t('Latest')}
                  </span>
                ) : null}
              </div>
              <time
                className='text-muted-foreground text-xs'
                dateTime={new Date(event.time * 1000).toISOString()}
              >
                {new Date(event.time * 1000).toLocaleString(locale)}
              </time>
              {event.evidence && (
                <div className='mt-1 flex flex-wrap gap-2'>
                  {challenge.issue_url && (
                    <a
                      className='border-border bg-background hover:bg-muted/50 inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-xs font-medium transition-colors motion-reduce:transition-none'
                      href={challenge.issue_url}
                      target='_blank'
                      rel='noreferrer'
                    >
                      <HugeiconsIcon
                        icon={LinkSquare02Icon}
                        strokeWidth={2}
                        aria-hidden='true'
                        className='size-3.5'
                      />
                      {t('Issue evidence')}
                    </a>
                  )}
                  {challenge.pull_request_url && (
                    <a
                      className='border-border bg-background hover:bg-muted/50 inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-xs font-medium transition-colors motion-reduce:transition-none'
                      href={challenge.pull_request_url}
                      target='_blank'
                      rel='noreferrer'
                    >
                      <HugeiconsIcon
                        icon={GitPullRequestIcon}
                        strokeWidth={2}
                        aria-hidden='true'
                        className='size-3.5'
                      />
                      {t('Pull request')}
                    </a>
                  )}
                </div>
              )}
            </li>
          )
        })}
      </ol>
      {challenge.status === 'accepted' && (
        <p className='text-muted-foreground mt-3'>
          {t('Next: complete the work and submit GitHub evidence.')}
        </p>
      )}
      {challenge.status === 'submitted' && !challenge.dispute && (
        <p className='text-warning mt-3 inline-flex items-center gap-1.5 font-medium'>
          <HugeiconsIcon
            icon={Clock01Icon}
            strokeWidth={2}
            aria-hidden='true'
            className='size-3.5'
          />
          {t('Waiting for publisher review')}
        </p>
      )}
      {(challenge.status === 'withdrawn' ||
        challenge.status === 'cancelled') && (
        <p className='text-muted-foreground mt-3'>
          {t(
            challenge.status === 'withdrawn'
              ? 'Withdrawn'
              : 'Cancelled by publisher'
          )}
        </p>
      )}
    </div>
  )
}
