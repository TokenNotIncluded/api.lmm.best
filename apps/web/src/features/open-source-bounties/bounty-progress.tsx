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
import { useTranslation } from 'react-i18next'

import { toIntlLocale } from '@/i18n/languages'

import { bountyTimeline } from './timeline'
import type { BountyChallenge } from './types'

export function BountyProgress({ challenge }: { challenge: BountyChallenge }) {
  const { t, i18n } = useTranslation()
  return (
    <div className='my-4 text-sm'>
      <h3 className='mb-3 font-semibold'>{t('Delivery timeline')}</h3>
      <ol className='border-border space-y-4 border-l pl-4'>
        {bountyTimeline(challenge).map((event) => (
          <li key={event.key} className='relative'>
            <span
              aria-hidden='true'
              className='bg-foreground absolute top-1.5 -left-[1.3125rem] size-2 rounded-full'
            />
            <div>{t(event.key)}</div>
            <time
              className='text-muted-foreground text-xs'
              dateTime={new Date(event.time * 1000).toISOString()}
            >
              {new Date(event.time * 1000).toLocaleString(
                toIntlLocale(i18n.language)
              )}
            </time>
            {event.evidence && (
              <div className='flex flex-wrap gap-4'>
                {challenge.issue_url && (
                  <a
                    className='py-1 underline'
                    href={challenge.issue_url}
                    target='_blank'
                    rel='noreferrer'
                  >
                    {t('Issue evidence')}
                  </a>
                )}
                {challenge.pull_request_url && (
                  <a
                    className='py-1 underline'
                    href={challenge.pull_request_url}
                    target='_blank'
                    rel='noreferrer'
                  >
                    {t('Pull request')}
                  </a>
                )}
              </div>
            )}
          </li>
        ))}
      </ol>
      {challenge.status === 'accepted' && (
        <p className='text-muted-foreground mt-3'>
          {t('Next: complete the work and submit GitHub evidence.')}
        </p>
      )}
      {challenge.status === 'submitted' && !challenge.dispute && (
        <p className='text-muted-foreground mt-3'>
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
