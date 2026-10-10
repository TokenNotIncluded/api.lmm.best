/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'

/**
 * One model's slice. `color` must be a CSS color the caller resolved from the
 * design tokens so the ring follows the active theme.
 */
export interface ModelUsageSegment {
  key: string
  label: string
  value: number
  color: string
  /** Short right-hand caption, e.g. "1.2M tokens". */
  detail?: string
}

interface ModelUsageDonutProps {
  segments: ModelUsageSegment[]
  /** Rendered in the middle of the ring when nothing is hovered. */
  centerValue: string
  centerLabel: string
  activeIndex: number | null
  onActiveIndexChange: (index: number | null) => void
  /**
   * Fired on click/tap and on Enter for the focused slice — the caller uses it
   * to focus the matching row in the leaderboard.
   */
  onSelect?: (index: number) => void
  /** Accessible name for the figure. */
  label: string
  className?: string
}

const SIZE = 260
const RADIUS = 104
const THICKNESS = 26
const GAP_RADIANS = 0.012

interface Arc {
  key: string
  path: string
  color: string
  label: string
  index: number
  /** Slice angle span, in radians, before the gap is subtracted. */
  span: number
}

function polarPoint(
  centerX: number,
  centerY: number,
  radius: number,
  angle: number
) {
  return {
    x: centerX + radius * Math.cos(angle),
    y: centerY + radius * Math.sin(angle),
  }
}

function buildArcPath(
  centerX: number,
  centerY: number,
  radius: number,
  startAngle: number,
  endAngle: number
): string {
  const start = polarPoint(centerX, centerY, radius, startAngle)
  const end = polarPoint(centerX, centerY, radius, endAngle)
  const largeArc = endAngle - startAngle > Math.PI ? 1 : 0
  // A full circle cannot be drawn as one arc (start and end coincide), so a
  // near-full slice gets a hairline cut instead of a broken path.
  return [
    `M ${start.x} ${start.y}`,
    `A ${radius} ${radius} 0 ${largeArc} 1 ${end.x} ${end.y}`,
  ].join(' ')
}

function buildArcs(segments: ModelUsageSegment[]): Arc[] {
  const total = segments.reduce(
    (sum, segment) => sum + Math.max(0, segment.value),
    0
  )
  if (total <= 0) return []

  const visible = segments.filter((segment) => segment.value > 0)
  // A gap only reads as a gap when the ring is not a single slice.
  const gap = visible.length > 1 ? GAP_RADIANS : 0
  const centerX = SIZE / 2
  const centerY = SIZE / 2
  let cursor = -Math.PI / 2

  return visible.map((segment) => {
    const span = (Math.max(0, segment.value) / total) * Math.PI * 2
    const startAngle = cursor + gap / 2
    const endAngle = cursor + Math.min(span, Math.PI * 2 - 0.0001) - gap / 2
    cursor += span

    // Segments under ~2° collapse into a dot rather than a hairline arc that
    // renders as an invisible smudge; they still get a full-size hit target.
    const drawable = endAngle > startAngle
    return {
      key: segment.key,
      path: drawable
        ? buildArcPath(centerX, centerY, RADIUS, startAngle, endAngle)
        : buildArcPath(
            centerX,
            centerY,
            RADIUS,
            startAngle,
            startAngle + 0.001
          ),
      color: segment.color,
      label: segment.label,
      index: segments.indexOf(segment),
      span,
    }
  })
}

/**
 * Interactive spending ring. Built as plain SVG (no chart dependency, no
 * layout thrash): hovering or focusing a slice highlights it and reports the
 * index upward, and clicking selects it. Click is not required for the data —
 * the leaderboard below carries every number in text.
 */
export function ModelUsageDonut(props: ModelUsageDonutProps) {
  const { t } = useTranslation()
  const arcs = buildArcs(props.segments)
  const total = props.segments.reduce(
    (sum, segment) => sum + Math.max(0, segment.value),
    0
  )
  const active =
    props.activeIndex !== null ? props.segments[props.activeIndex] : undefined
  const activeShare =
    active && total > 0 ? Math.max(0, active.value) / total : 0
  const percentage = new Intl.NumberFormat(undefined, {
    style: 'percent',
    maximumFractionDigits: 1,
  }).format(activeShare)

  return (
    <figure className={cn('flex flex-col items-center gap-3', props.className)}>
      <div className='relative'>
        <svg
          viewBox={`0 0 ${SIZE} ${SIZE}`}
          role='img'
          aria-label={props.label}
          className='size-[220px] sm:size-[260px]'
        >
          <circle
            cx={SIZE / 2}
            cy={SIZE / 2}
            r={RADIUS}
            fill='none'
            strokeWidth={THICKNESS}
            className='stroke-muted/50'
          />
          {arcs.map((arc) => {
            const isActive = props.activeIndex === arc.index
            const isDimmed =
              props.activeIndex !== null && !isActive && arc.span < Math.PI
            return (
              <g key={arc.key}>
                {/* Invisible fat stroke: mobile taps should never need pixel aim. */}
                <path
                  d={arc.path}
                  fill='none'
                  stroke='transparent'
                  strokeWidth={THICKNESS + 22}
                  strokeLinecap='round'
                  className='cursor-pointer'
                  onPointerEnter={() => props.onActiveIndexChange(arc.index)}
                  onPointerLeave={() => props.onActiveIndexChange(null)}
                  onClick={() => props.onSelect?.(arc.index)}
                />
                <path
                  d={arc.path}
                  fill='none'
                  stroke={arc.color}
                  strokeWidth={THICKNESS}
                  strokeLinecap='round'
                  className={cn(
                    'pointer-events-none transition-opacity duration-200 motion-reduce:transition-none',
                    isDimmed ? 'opacity-45' : 'opacity-100'
                  )}
                />
              </g>
            )
          })}
        </svg>
        <div className='pointer-events-none absolute inset-0 flex flex-col items-center justify-center gap-0.5 px-10 text-center'>
          <span className='text-foreground max-w-full text-sm font-semibold tracking-tight text-balance break-all tabular-nums sm:text-base'>
            {active ? active.label : props.centerValue}
          </span>
          <span className='text-muted-foreground max-w-full text-[11px] leading-tight'>
            {active
              ? [percentage, active.detail].filter(Boolean).join(' · ')
              : props.centerLabel}
          </span>
        </div>
      </div>

      {/*
        The ring is decorative for assistive tech; this table is the real
        content, and it is also what makes the chart usable without a pointer.
      */}
      <figcaption className='sr-only'>
        <table>
          <caption>{props.label}</caption>
          <thead>
            <tr>
              <th scope='col'>{t('Model')}</th>
              <th scope='col'>{t('Share')}</th>
            </tr>
          </thead>
          <tbody>
            {props.segments.map((segment) => (
              <tr key={segment.key}>
                <th scope='row'>{segment.label}</th>
                <td>
                  {total > 0
                    ? new Intl.NumberFormat(undefined, {
                        style: 'percent',
                        maximumFractionDigits: 1,
                      }).format(Math.max(0, segment.value) / total)
                    : '0%'}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </figcaption>
    </figure>
  )
}
