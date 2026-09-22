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
import { formatQuota } from '@/lib/format'

import { bountyAvailableSlots } from './timeline'
import type { BountyProject } from './types'

export function BountyDecision({
  project,
  compact = false,
}: {
  project: BountyProject
  compact?: boolean
}) {
  const { t, i18n } = useTranslation()
  return (
    <div className='space-y-3 text-xs leading-5'>
      <dl className='grid grid-cols-2 gap-x-4 gap-y-2'>
        <div>
          <dt className='text-muted-foreground'>{t('Available slots')}</dt>
          <dd>
            {bountyAvailableSlots(project)} / {project.reward_slots}
          </dd>
        </div>
        <div>
          <dt className='text-muted-foreground'>{t('Reserved slots')}</dt>
          <dd>{project.active_challenge_count}</dd>
        </div>
        <div>
          <dt className='text-muted-foreground'>{t('Publisher reputation')}</dt>
          <dd>
            {project.owner_rating_count > 0
              ? `${project.owner_rating_average.toFixed(1)} / 5 (${project.owner_rating_count})`
              : t('No ratings yet')}
          </dd>
        </div>
        <div>
          <dt className='text-muted-foreground'>{t('Updated')}</dt>
          <dd>
            {project.updated_at > 0
              ? new Date(project.updated_at * 1000).toLocaleDateString(
                  toIntlLocale(i18n.resolvedLanguage || i18n.language)
                )
              : t('Unknown')}
          </dd>
        </div>
      </dl>
      {project.accepted_challenge_count !== undefined &&
        project.submitted_challenge_count !== undefined && (
          <p>
            {t('Active deliveries')}:{' '}
            {project.accepted_challenge_count +
              project.submitted_challenge_count}
          </p>
        )}
      <p>{t('Evidence: Issue or PR; follow the acceptance rules.')}</p>
      <p className='font-medium'>
        {t('Rewards are credited to your API account balance.')}
      </p>
      {!compact && (
        <>
          <p>
            {t('Reviewed by the publisher: {{name}}', {
              name: project.owner_username,
            })}
          </p>
          <p>
            {t('Current escrow: {{amount}} in API account balance.', {
              amount: formatQuota(project.escrow_quota),
            })}
          </p>
          <p>
            {t(
              'If rejected, open a dispute within 7 days. A platform administrator reviews the evidence.'
            )}
          </p>
        </>
      )}
    </div>
  )
}
