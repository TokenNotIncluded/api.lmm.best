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

import { StatusBadge } from '@/components/status-badge'
import { Progress } from '@/components/ui/progress'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
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
import {
  formatCumulativeUserUsage,
  formatRawCreditCount,
  normalizedUserUsage,
} from '@/lib/cumulative-user-usage'
import { getCurrencyFormattingLocale } from '@/lib/currency'
import { formatQuota } from '@/lib/format'
import { cn } from '@/lib/utils'

type UserQuotaCellProps = {
  used: number
  normalizedUsed?: number | null
  projectionAvailable?: boolean
  transferred?: number
  remaining: number
}

function getQuotaProgressColor(percentage: number): string {
  if (percentage <= 10) return 'console-status-progress-danger'
  if (percentage <= 30) return 'console-status-progress-warning'
  return 'console-status-progress-success'
}

export function UserQuotaCell(props: UserQuotaCellProps) {
  const { t, i18n } = useTranslation()
  const rawCreditLocale = getCurrencyFormattingLocale(
    i18n.resolvedLanguage || i18n.language
  )
  const usage = {
    used_quota: props.used,
    normalized_used_quota: props.normalizedUsed,
    usage_projection_available: props.projectionAvailable,
  }
  const normalized = normalizedUserUsage(usage)
  const total =
    normalized === null || (props.transferred ?? 0) !== 0
      ? null
      : normalized + props.remaining
  const percentage =
    total !== null && total > 0 ? (props.remaining / total) * 100 : 0
  const formattedRemaining = formatQuota(props.remaining)
  const formattedTotal = total === null ? t('Unavailable') : formatQuota(total)

  if (total === 0) {
    return (
      <StatusBadge
        label={t('No Quota')}
        variant='neutral'
        copyable={false}
        className='-ml-1.5'
      />
    )
  }

  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <div className='w-full min-w-0 cursor-help space-y-1.5 overflow-hidden' />
        }
      >
        <div className='grid min-w-0 grid-cols-2 gap-x-4 text-xs'>
          <span className='min-w-0 truncate font-medium tabular-nums'>
            {formattedRemaining}
          </span>
          <span className='text-muted-foreground min-w-0 truncate text-right tabular-nums'>
            {formattedTotal}
          </span>
        </div>
        {total !== null && (
          <Progress
            value={percentage}
            className={cn('h-1.5', getQuotaProgressColor(percentage))}
          />
        )}
      </TooltipTrigger>
      <TooltipContent>
        <div className='space-y-1 text-xs'>
          <div>
            {t('Used:')}{' '}
            {formatCumulativeUserUsage(
              usage,
              formatQuota,
              t('Credits'),
              rawCreditLocale
            )}
          </div>
          <div>
            {t('Remaining:')} {formattedRemaining}
          </div>
          <div>
            {t('Transferred out')}:{' '}
            {formatRawCreditCount(
              props.transferred ?? 0,
              t('Credits'),
              rawCreditLocale
            )}
          </div>
          <div>
            {t('Total:')} {formattedTotal}
          </div>
          <div>
            {t('Percentage:')}{' '}
            {total === null ? t('Unavailable') : `${percentage.toFixed(1)}%`}
          </div>
        </div>
      </TooltipContent>
    </Tooltip>
  )
}
