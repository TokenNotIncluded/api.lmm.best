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
import { useTranslation } from 'react-i18next'

import { toIntlLocale } from '@/i18n/languages'
import { formatQuota } from '@/lib/format'
import { cn } from '@/lib/utils'

import { bountyAvailableSlots } from './timeline'
import type { BountyProject } from './types'

function Fact({
  label,
  value,
  emphasis = false,
}: {
  label: string
  value: React.ReactNode
  emphasis?: boolean
}) {
  return (
    <div className='border-border/60 bg-background rounded-lg border px-3 py-2'>
      <dt className='text-muted-foreground text-xs'>{label}</dt>
      <dd
        className={cn(
          'mt-0.5 text-sm tabular-nums',
          emphasis ? 'font-semibold' : 'font-medium'
        )}
      >
        {value}
      </dd>
    </div>
  )
}

export function BountyDecision({
  project,
  compact = false,
}: {
  project: BountyProject
  compact?: boolean
}) {
  const { t, i18n } = useTranslation()
  const reputation =
    project.owner_rating_count > 0
      ? `${project.owner_rating_average.toFixed(1)} / 5 (${project.owner_rating_count})`
      : t('No ratings yet')
  const updated =
    project.updated_at > 0
      ? new Date(project.updated_at * 1000).toLocaleDateString(
          toIntlLocale(i18n.resolvedLanguage || i18n.language)
        )
      : t('Unknown')

  return (
    <div className='space-y-3 text-xs leading-5'>
      <dl className='grid grid-cols-2 gap-2'>
        <Fact
          label={t('Available slots')}
          value={`${bountyAvailableSlots(project)} / ${project.reward_slots}`}
          emphasis
        />
        <Fact
          label={t('Reserved slots')}
          value={project.active_challenge_count}
        />
        <Fact label={t('Publisher reputation')} value={reputation} />
        <Fact label={t('Updated')} value={updated} />
      </dl>
      {project.accepted_challenge_count !== undefined &&
        project.submitted_challenge_count !== undefined && (
          <p>
            {t('Active deliveries')}:{' '}
            <strong className='font-medium tabular-nums'>
              {project.accepted_challenge_count +
                project.submitted_challenge_count}
            </strong>
          </p>
        )}
      <div className='border-border/60 bg-muted/20 space-y-1.5 rounded-lg border px-3 py-2'>
        <p>{t('Evidence: Issue or PR; follow the acceptance rules.')}</p>
        <p className='font-medium'>
          {t('Rewards are credited to your API account balance.')}
        </p>
      </div>
      {!compact && (
        <div className='space-y-1.5'>
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
        </div>
      )}
    </div>
  )
}
