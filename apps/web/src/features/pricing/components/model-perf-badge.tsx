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
import { memo } from 'react'
import { useTranslation } from 'react-i18next'

import {
  getSuccessRateDotClass,
  getSuccessRateTextClass,
} from '@/features/performance-metrics/lib/format'
import { cn } from '@/lib/utils'

import { getModelPerfDisplay, type ModelPerfBadgeData } from '../lib/model-perf'

export interface ModelPerfBadgeProps extends React.HTMLAttributes<HTMLDivElement> {
  perf: ModelPerfBadgeData | undefined
}

const STATUS_BAR_SLOTS = [
  { key: 'oldest', heightClass: 'h-2' },
  { key: 'middle', heightClass: 'h-2.5' },
  { key: 'latest', heightClass: 'h-3' },
] as const

function getEmptyStatusBarClass(slotIndex: number): string {
  return slotIndex === 0 ? 'bg-muted-foreground/10' : 'bg-muted-foreground/15'
}

export function ModelPerfStatus(props: {
  perf: ModelPerfBadgeData | undefined
  className?: string
}) {
  const { t } = useTranslation()
  const display = getModelPerfDisplay(props.perf)
  const successRate = props.perf?.success_rate ?? Number.NaN
  const label = `${t('Success rate')} · 24h · ${t('All Groups')}: ${display.successRate}`

  return (
    <div
      title={label}
      aria-label={label}
      className={cn('flex h-4 min-w-0 items-center gap-1', props.className)}
    >
      <div aria-hidden='true' className='flex items-center gap-0.5'>
        {STATUS_BAR_SLOTS.map((slot, slotIndex) => {
          const rate = display.statusBars[slotIndex]
          return (
            <span
              key={slot.key}
              className={cn(
                'w-1 rounded-full',
                slot.heightClass,
                rate == null
                  ? getEmptyStatusBarClass(slotIndex)
                  : getSuccessRateDotClass(rate)
              )}
            />
          )
        })}
      </div>
      <span
        className={cn(
          'font-mono text-[10px] leading-4 whitespace-nowrap',
          Number.isFinite(successRate)
            ? getSuccessRateTextClass(successRate)
            : 'text-muted-foreground'
        )}
      >
        {display.successRate}
      </span>
    </div>
  )
}

export const ModelPerfBadge = memo(function ModelPerfBadge(
  props: ModelPerfBadgeProps
) {
  const { t } = useTranslation()
  const display = getModelPerfDisplay(props.perf)
  const latencyLabel = `${t('Average latency')} · 24h · ${t('All Groups')}: ${display.latency}`
  const throughputLabel = `${t('Throughput')} · 24h · ${t('All Groups')}: ${display.throughput}`

  return (
    <div
      className={cn(
        'grid w-full grid-cols-3 gap-x-3 text-left tabular-nums min-[460px]:w-[170px] min-[460px]:grid-cols-[44px_56px_54px] min-[460px]:gap-x-2 min-[460px]:text-right',
        props.className
      )}
    >
      <div title={latencyLabel} aria-label={latencyLabel} className='min-w-0'>
        <div className='text-muted-foreground truncate text-[11px] leading-4 font-medium'>
          {t('Latency short')}
        </div>
        <div className='text-foreground/80 font-mono text-xs leading-4 whitespace-nowrap'>
          {display.latency}
        </div>
      </div>
      <div
        title={throughputLabel}
        aria-label={throughputLabel}
        className='min-w-0'
      >
        <div className='text-muted-foreground truncate text-[11px] leading-4 font-medium'>
          {t('Throughput short')}
        </div>
        <div className='text-foreground/80 font-mono text-xs leading-4 whitespace-nowrap'>
          {display.throughput}
        </div>
      </div>
      <div className='min-w-0'>
        <div className='text-muted-foreground truncate text-[11px] leading-4 font-medium'>
          {t('Status short')}
        </div>
        <ModelPerfStatus
          perf={props.perf}
          className='min-[460px]:justify-end'
        />
      </div>
    </div>
  )
})
