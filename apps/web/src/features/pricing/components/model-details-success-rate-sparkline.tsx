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

import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import {
  getSuccessRateDotClass,
  getSuccessRateTextClass,
} from '@/features/performance-metrics/lib/format'
import { cn } from '@/lib/utils'

import type { SuccessRateTimePoint } from '../lib/performance-series'

// Each bar represents an observed API sample, not a day or an outage.

type SparklineSize = 'sm' | 'md'

type SuccessRateSparklineProps = {
  series: SuccessRateTimePoint[]
  size?: SparklineSize
  showOverall?: boolean
  emptyLabel?: string
  className?: string
}

export function SuccessRateSparkline(props: SuccessRateSparklineProps) {
  const size = props.size ?? 'md'
  const showOverall = props.showOverall ?? true
  const { t } = useTranslation()

  if (props.series.length === 0) {
    return (
      <span className={cn('text-muted-foreground text-xs', props.className)}>
        {props.emptyLabel ?? '—'}
      </span>
    )
  }

  const overall =
    props.series.reduce((s, p) => s + p.success_rate, 0) / props.series.length

  const containerHeight = size === 'sm' ? 'h-3.5' : 'h-5'
  const barWidth = size === 'sm' ? 'w-[3px]' : 'w-1'
  const gap = size === 'sm' ? 'gap-px' : 'gap-[2px]'

  return (
    <div className={cn('flex items-center gap-2', props.className)}>
      <div
        className={cn('flex items-end', containerHeight, gap)}
        role='img'
        aria-label={`${t('Success rate')} ${overall.toFixed(2)}%`}
      >
        {props.series.map((point) => (
          <Tooltip key={point.timestamp}>
            <TooltipTrigger
              render={
                <div
                  className={cn(
                    'rounded-sm transition-opacity hover:opacity-80',
                    barWidth,
                    containerHeight,
                    'flex items-end'
                  )}
                />
              }
            >
              <div
                className={cn(
                  'w-full rounded-sm',
                  getSuccessRateDotClass(point.success_rate)
                )}
                style={{ height: `${point.success_rate}%`, minHeight: 1 }}
                aria-hidden
              />
            </TooltipTrigger>
            <TooltipContent side='top' className='font-mono text-xs'>
              <div className='font-medium'>{point.timestamp}</div>
              <div>{point.success_rate.toFixed(2)}%</div>
            </TooltipContent>
          </Tooltip>
        ))}
      </div>
      {showOverall && (
        <span
          className={cn(
            'font-mono text-sm font-semibold tabular-nums',
            getSuccessRateTextClass(overall)
          )}
        >
          {overall.toFixed(1)}%
        </span>
      )}
    </div>
  )
}
