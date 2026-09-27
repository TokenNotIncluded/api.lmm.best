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
import { motion, useReducedMotion } from 'motion/react'
import { useTranslation } from 'react-i18next'

import { getLobeIcon } from '@/lib/lobe-icon'
import { cn } from '@/lib/utils'

import { formatTokens } from '../lib/format'
import type { ModelRanking } from '../types'
import { ModelLink, VendorLink } from './entity-links'
import { GrowthText } from './growth-text'

type ModelLeaderboardProps = {
  rows: ModelRanking[]
  /** Density variant. `compact` is used inside per-category sections; the
   * default fits the larger overall "Top Models" section. */
  variant?: 'default' | 'compact'
  /** Optional cap (rows beyond this are dropped). */
  limit?: number
}

/** Medal accent for the first three ranks; the rest stay neutral. */
const RANK_ACCENT: Record<number, string> = {
  1: 'text-amber-500',
  2: 'text-slate-400',
  3: 'text-orange-700 dark:text-orange-500',
}

/**
 * Ranked model list rendering: "rank · model (vendor below) · bar · tokens
 * (growth below)". Each row draws a share bar proportional to the model's
 * slice of all routed tokens, so the shape of the leaderboard is readable at
 * a glance instead of requiring the reader to compare numbers.
 *
 * Both the model name and vendor name are clickable: model jumps to
 * `/pricing/{modelName}` and vendor jumps to `/pricing?vendor={vendor}`.
 */
export function ModelLeaderboard(props: ModelLeaderboardProps) {
  const limited = props.limit ? props.rows.slice(0, props.limit) : props.rows
  const half = Math.ceil(limited.length / 2)
  const left = limited.slice(0, half)
  const right = limited.slice(half)
  const variant = props.variant ?? 'default'
  const maxShare = limited.reduce(
    (peak, row) =>
      Number.isFinite(row.share) ? Math.max(peak, row.share) : peak,
    0
  )

  if (limited.length === 0) {
    return null
  }

  return (
    <div className='grid grid-cols-1 gap-x-8 md:grid-cols-2'>
      <ModelList rows={left} variant={variant} maxShare={maxShare} />
      {right.length > 0 && (
        <ModelList rows={right} variant={variant} maxShare={maxShare} />
      )}
    </div>
  )
}

function ModelList(props: {
  rows: ModelRanking[]
  variant: 'default' | 'compact'
  maxShare: number
}) {
  const { t } = useTranslation()
  const shouldReduce = useReducedMotion()
  const compact = props.variant === 'compact'
  return (
    <ul>
      {props.rows.map((row, index) => {
        const share = Number.isFinite(row.share) ? row.share : 0
        const width =
          props.maxShare > 0
            ? Math.max(2, Math.min(100, (share / props.maxShare) * 100))
            : 0
        const delta =
          row.previous_rank === undefined ? null : row.previous_rank - row.rank
        return (
          <li
            key={row.model_name}
            className={cn(
              'group relative flex items-center gap-3 rounded-md px-1 transition-colors hover:bg-muted/40',
              compact ? 'py-2' : 'py-2.5'
            )}
          >
            <span
              className={cn(
                'w-6 shrink-0 text-right font-mono tabular-nums',
                compact ? 'text-xs' : 'text-sm',
                RANK_ACCENT[row.rank] ?? 'text-muted-foreground/80',
                RANK_ACCENT[row.rank] && 'font-semibold'
              )}
            >
              {row.rank}.
            </span>
            <span className='shrink-0'>
              {getLobeIcon(row.vendor_icon, compact ? 20 : 22)}
            </span>
            <div className='min-w-0 flex-1'>
              <div className='flex items-baseline gap-1.5'>
                <ModelLink
                  modelName={row.model_name}
                  className={cn(
                    'text-foreground block min-w-0 truncate font-mono font-medium',
                    compact ? 'text-xs' : 'text-sm'
                  )}
                >
                  {row.model_name}
                </ModelLink>
                {delta !== null && delta !== 0 && (
                  <span
                    className={cn(
                      'shrink-0 font-mono text-[10px] tabular-nums',
                      delta > 0 ? 'text-emerald-600' : 'text-rose-600'
                    )}
                    title={
                      delta > 0
                        ? t('Up {{count}} places', { count: delta })
                        : t('Down {{count}} places', { count: -delta })
                    }
                  >
                    {delta > 0 ? '▲' : '▼'}
                    {Math.abs(delta)}
                  </span>
                )}
              </div>
              <p
                className={cn(
                  'text-muted-foreground/80 truncate italic',
                  compact ? 'text-[11px]' : 'text-xs'
                )}
              >
                {t('by')}{' '}
                <VendorLink vendor={row.vendor}>
                  {row.vendor.toLowerCase()}
                </VendorLink>
              </p>
              <div
                aria-hidden='true'
                className='bg-foreground/5 mt-1.5 h-1 w-full overflow-hidden rounded-full'
              >
                <motion.div
                  className='h-full rounded-full bg-[linear-gradient(90deg,var(--primary),color-mix(in_oklab,var(--primary)_45%,transparent))]'
                  initial={shouldReduce ? false : { width: 0 }}
                  whileInView={{ width: `${width}%` }}
                  viewport={{ once: true, amount: 0.4 }}
                  transition={{
                    duration: 0.45,
                    delay: Math.min(index, 8) * 0.02,
                    ease: 'easeOut',
                  }}
                  style={shouldReduce ? { width: `${width}%` } : undefined}
                />
              </div>
            </div>
            <div className='shrink-0 text-right'>
              <div
                className={cn(
                  'text-foreground font-mono font-semibold tabular-nums',
                  compact ? 'text-xs' : 'text-sm'
                )}
              >
                {formatTokens(row.total_tokens)}
                {!compact && (
                  <>
                    {' '}
                    <span className='text-muted-foreground/80 font-normal'>
                      {t('tokens')}
                    </span>
                  </>
                )}
              </div>
              <div className='flex items-center justify-end gap-2'>
                <GrowthText
                  value={row.growth_pct}
                  className={compact ? 'text-[10px]' : 'text-[11px]'}
                />
                <span
                  className={cn(
                    'text-muted-foreground/70 font-mono tabular-nums',
                    compact ? 'text-[10px]' : 'text-[11px]'
                  )}
                >
                  {t('{{share}}% share', {
                    share: (share * 100).toFixed(share < 0.01 ? 2 : 1),
                  })}
                </span>
              </div>
            </div>
          </li>
        )
      })}
    </ul>
  )
}
