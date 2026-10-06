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

import { Button } from '@/components/ui/button'
import {
  Popover,
  PopoverContent,
  PopoverDescription,
  PopoverTitle,
  PopoverTrigger,
} from '@/components/ui/popover'
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
import { formatRawCreditCount } from '@/lib/cumulative-user-usage'

import type { User } from '../types'

const reasonLabels: Record<string, string> = {
  checkin_transfer_only:
    'Check-ins, zero consumption, outgoing transfers and no paid or incoming funds',
  outbound_without_usage: 'Outgoing transfers without consumption',
  transfer_dominates_usage:
    'Transfers exceed consumption by more than four times',
  checkin_without_usage: 'Check-ins without consumption',
  received_from_high_risk: 'Received funds from a high-risk sender',
  moderation_violations: 'Moderation violations in user input',
}
export function UserRiskCell({ user }: { user: User }) {
  const { t } = useTranslation()
  const risk = user.wallet_risk
  if (!risk) return <span className='text-muted-foreground'>—</span>
  const label = risk.high_risk
    ? 'High risk'
    : risk.score >= 0.4
      ? 'Medium risk'
      : 'Low risk'
  return (
    <Popover>
      <PopoverTrigger
        render={
          <Button
            variant='ghost'
            className='h-11 gap-2 px-1 sm:h-9'
            aria-label={`${t('Risk score')} ${risk.score.toFixed(3)}`}
          />
        }
      >
        <span className='font-medium tabular-nums'>
          {risk.score.toFixed(3)}
        </span>
        <span
          className={
            risk.high_risk
              ? 'text-red-700 dark:text-red-300'
              : risk.score >= 0.4
                ? 'text-amber-800 dark:text-amber-200'
                : 'text-muted-foreground'
          }
        >
          {t(label)}
        </span>
      </PopoverTrigger>
      <PopoverContent align='start' className='max-w-[calc(100vw-2rem)] gap-3'>
        <PopoverTitle>
          {t('Risk score')} · {risk.score.toFixed(3)}
        </PopoverTitle>
        <PopoverDescription>
          {t(
            'A rule-based estimate, not a calibrated probability. Updated as account activity changes.'
          )}
        </PopoverDescription>
        <ul className='list-disc space-y-1 pl-4'>
          {risk.reasons.length ? (
            risk.reasons.map((reason) => (
              <li key={reason}>{t(reasonLabels[reason] || reason)}</li>
            ))
          ) : (
            <li>{t('No risk signals')}</li>
          )}
        </ul>
        <dl className='grid grid-cols-2 gap-2 text-xs'>
          <dt>{t('Check-in rewards')}</dt>
          <dd className='text-right tabular-nums'>
            {formatRawCreditCount(risk.checkin_quota, t('Credits'))}
          </dd>
          <dt>{t('Transferred out')}</dt>
          <dd className='text-right tabular-nums'>
            {formatRawCreditCount(risk.transferred_quota, t('Credits'))}
          </dd>
          <dt>{t('Pending transfers')}</dt>
          <dd className='text-right tabular-nums'>
            {formatRawCreditCount(risk.pending_quota, t('Credits'))}
          </dd>
          <dt>{t('Received transfers')}</dt>
          <dd className='text-right tabular-nums'>
            {formatRawCreditCount(risk.received_quota, t('Credits'))}
          </dd>
          <dt>{t('High-risk senders')}</dt>
          <dd className='text-right tabular-nums'>{risk.high_risk_senders}</dd>
          {risk.moderation_reviewed_count !== undefined ? (
            <>
              <dt>{t('Moderation reviews')}</dt>
              <dd className='text-right tabular-nums'>
                {risk.moderation_reviewed_count}
              </dd>
            </>
          ) : null}
          {risk.moderation_flagged_count !== undefined ? (
            <>
              <dt>{t('Flagged user inputs')}</dt>
              <dd className='text-right tabular-nums'>
                {risk.moderation_flagged_count}
              </dd>
            </>
          ) : null}
        </dl>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Pending transfers count as sent; cancelled transfers are excluded. High risk starts at 0.8. No automatic bans.'
          )}
        </p>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Only flagged user input contributes to Moderation risk. Model output is excluded.'
          )}
        </p>
      </PopoverContent>
    </Popover>
  )
}
