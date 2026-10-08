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
import { VChart } from '@visactor/react-vchart'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { getSuccessRateColor } from '@/features/performance-metrics/lib/format'
import { resolveForgeColor } from '@/lib/forge-colors'
import { useChartTheme } from '@/lib/use-chart-theme'
import { cn } from '@/lib/utils'
import { VCHART_OPTION } from '@/lib/vchart'

import type {
  LatencyTimePoint,
  SuccessRateTimePoint,
} from '../lib/performance-series'

function formatHourLabel(iso: string): string {
  const date = new Date(iso)
  const hours = date.getHours()
  return `${String(hours).padStart(2, '0')}:00`
}

function formatSampleLabel(date: string): string {
  const parsed = new Date(date)
  if (date.includes('T')) {
    return parsed.toLocaleString(undefined, {
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
    })
  }
  return parsed.toLocaleDateString(undefined, {
    month: 'short',
    day: 'numeric',
  })
}

function getChartThemeTokens(resolvedTheme: string) {
  const isDark = resolvedTheme === 'dark'
  return {
    textColor: resolveForgeColor(
      isDark ? '--forge-chart-text-dark' : '--forge-chart-text-light'
    ),
    gridColor: resolveForgeColor(
      isDark ? '--forge-chart-grid-dark' : '--forge-chart-grid-light'
    ),
    pointColor: resolveForgeColor(
      isDark ? '--forge-chart-point-dark' : '--forge-chart-point-light'
    ),
    seriesColor: resolveForgeColor(
      isDark ? '--forge-chart-series-dark' : '--forge-chart-series-light'
    ),
  }
}

const SUCCESS_RATE_AXIS_MAX = 100
const SUCCESS_RATE_FOCUSED_AXIS_MIN = 95
const SUCCESS_RATE_WIDE_AXIS_MIN = 90

function toSuccessRateChartValue(value: number): number {
  if (!Number.isFinite(value)) return 0
  return Math.min(SUCCESS_RATE_AXIS_MAX, Math.max(0, value))
}

function getSuccessRateAxisMin(values: number[]): number {
  const finiteValues = values.filter((value) => Number.isFinite(value))
  if (finiteValues.length === 0) return SUCCESS_RATE_FOCUSED_AXIS_MIN

  const minValue = Math.max(0, Math.min(...finiteValues))
  if (minValue >= SUCCESS_RATE_FOCUSED_AXIS_MIN) {
    return SUCCESS_RATE_FOCUSED_AXIS_MIN
  }
  if (minValue >= SUCCESS_RATE_WIDE_AXIS_MIN) {
    return SUCCESS_RATE_WIDE_AXIS_MIN
  }

  return Math.max(0, Math.floor((minValue - 5) / 10) * 10)
}

// ---------------------------------------------------------------------------
// Latency trend chart (24h, multi-group point-line chart)
// ---------------------------------------------------------------------------

export function LatencyTrendChart(props: {
  series: LatencyTimePoint[]
  className?: string
}) {
  const { t } = useTranslation()
  const { resolvedTheme, themeReady } = useChartTheme()
  const { textColor, gridColor, pointColor } =
    getChartThemeTokens(resolvedTheme)

  const spec = useMemo(() => {
    if (props.series.length === 0) return null
    const data = props.series.map((point) => ({
      time: point.timestamp,
      group: point.group,
      ttft: point.ttft_ms,
    }))
    return {
      type: 'line' as const,
      data: [{ id: 'latency', values: data }],
      xField: 'time',
      yField: 'ttft',
      seriesField: 'group',
      smooth: true,
      point: {
        visible: true,
        style: { size: 5, stroke: pointColor, lineWidth: 1.5 },
      },
      line: {
        style: { lineWidth: 2 },
      },
      legends: { visible: false },
      tooltip: {
        mark: {
          title: { value: (d: { time: string }) => formatSampleLabel(d.time) },
          content: [
            {
              key: t('Average TTFT'),
              value: (d: { ttft: number }) => `${Math.round(d.ttft)} ms`,
            },
          ],
        },
      },
      axes: [
        {
          orient: 'bottom',
          label: {
            formatMethod: (val: number | string) =>
              formatHourLabel(String(val)),
            style: { fill: textColor, fontSize: 10 },
          },
          tick: { visible: false },
        },
        {
          orient: 'left',
          label: {
            formatMethod: (val: number | string) => `${val} ms`,
            style: { fill: textColor, fontSize: 10 },
          },
          grid: {
            visible: true,
            style: { lineDash: [3, 3], stroke: gridColor },
          },
        },
      ],
    }
  }, [gridColor, pointColor, props.series, t, textColor])

  if (props.series.length === 0) {
    return (
      <div
        className={cn(
          'text-muted-foreground flex h-48 items-center justify-center rounded-lg border text-xs',
          props.className
        )}
      >
        {t('No latency data available')}
      </div>
    )
  }

  return (
    <div className={cn('h-64 sm:h-72', props.className)}>
      {themeReady && spec && (
        <VChart
          key={`latency-${resolvedTheme}`}
          spec={{
            ...spec,
            theme: resolvedTheme === 'dark' ? 'dark' : 'light',
            background: 'transparent',
          }}
          option={VCHART_OPTION}
        />
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Request success-rate trend (observed API samples)
// ---------------------------------------------------------------------------

export function SuccessRateTrendChart(props: {
  series: SuccessRateTimePoint[]
  className?: string
}) {
  const { t } = useTranslation()
  const { resolvedTheme, themeReady } = useChartTheme()
  const { textColor, gridColor, pointColor, seriesColor } =
    getChartThemeTokens(resolvedTheme)

  const spec = useMemo(() => {
    if (props.series.length === 0) return null

    const data = props.series.map((point) => ({
      date: point.timestamp,
      successRate: toSuccessRateChartValue(point.success_rate),
    }))
    const axisMin = getSuccessRateAxisMin(
      data.map((point) => point.successRate)
    )

    return {
      type: 'line' as const,
      data: [{ id: 'success-rate', values: data }],
      xField: 'date',
      yField: 'successRate',
      smooth: true,
      line: {
        style: { stroke: seriesColor, lineWidth: 2 },
      },
      point: {
        visible: true,
        style: {
          size: 5,
          stroke: pointColor,
          lineWidth: 1.5,
          fill: (datum: { successRate: number }) =>
            getSuccessRateColor(datum.successRate),
        },
      },
      tooltip: {
        mark: {
          title: {
            value: (d: { date: string }) => formatSampleLabel(d.date),
          },
          content: [
            {
              key: t('Success rate'),
              value: (d: { successRate: number }) =>
                `${d.successRate.toFixed(2)}%`,
            },
          ],
        },
      },
      axes: [
        {
          orient: 'bottom',
          label: {
            formatMethod: (val: number | string) =>
              formatSampleLabel(String(val)),
            style: { fill: textColor, fontSize: 10 },
            autoLimit: true,
          },
          tick: { visible: false },
        },
        {
          orient: 'left',
          min: axisMin,
          max: SUCCESS_RATE_AXIS_MAX,
          label: {
            formatMethod: (val: number | string) => `${val}%`,
            style: { fill: textColor, fontSize: 10 },
          },
          grid: {
            visible: true,
            style: { lineDash: [3, 3], stroke: gridColor },
          },
        },
      ],
    }
  }, [gridColor, pointColor, props.series, seriesColor, t, textColor])

  if (props.series.length === 0) {
    return (
      <div
        className={cn(
          'text-muted-foreground flex h-48 items-center justify-center rounded-lg border text-xs',
          props.className
        )}
      >
        {t('Performance data is not yet available for this model.')}
      </div>
    )
  }

  return (
    <div className={cn('h-56 sm:h-64', props.className)}>
      {themeReady && spec && (
        <VChart
          key={`successRate-trend-${resolvedTheme}`}
          spec={{
            ...spec,
            theme: resolvedTheme === 'dark' ? 'dark' : 'light',
            background: 'transparent',
          }}
          option={VCHART_OPTION}
        />
      )}
    </div>
  )
}
