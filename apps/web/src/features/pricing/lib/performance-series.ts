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
import type {
  PerformanceGroup,
  PerformanceSeriesPoint,
} from '@/features/performance-metrics/types'

export type LatencyTimePoint = {
  timestamp: string
  group: string
  ttft_ms: number
}

export type SuccessRateTimePoint = {
  timestamp: string
  success_rate: number
}

// The API supplies group means, not request counts. Average observed groups at
// each timestamp; never infer incidents, downtime, or missing observations.
function meanSeries(
  groups: PerformanceGroup[],
  readValue: (point: PerformanceSeriesPoint) => number | null
) {
  const buckets = new Map<number, { total: number; count: number }>()
  for (const group of groups) {
    for (const point of group.series) {
      if (
        !Number.isFinite(point.ts) ||
        !Number.isFinite(new Date(point.ts * 1000).getTime())
      ) {
        continue
      }
      const value = readValue(point)
      if (value === null) continue
      const bucket = buckets.get(point.ts) ?? { total: 0, count: 0 }
      bucket.total += value
      bucket.count += 1
      buckets.set(point.ts, bucket)
    }
  }
  return [...buckets.entries()]
    .sort(([a], [b]) => a - b)
    .map(([ts, bucket]) => ({
      timestamp: new Date(ts * 1000).toISOString(),
      value: bucket.total / bucket.count,
    }))
}

export function toLatencySeries(
  groups: PerformanceGroup[]
): LatencyTimePoint[] {
  return meanSeries(groups, (point) =>
    Number.isFinite(point.avg_ttft_ms) && point.avg_ttft_ms > 0
      ? point.avg_ttft_ms
      : null
  ).map(({ timestamp, value }) => ({
    timestamp,
    group: 'latency',
    ttft_ms: Math.round(value),
  }))
}

export function toSuccessRateSeries(
  groups: PerformanceGroup[]
): SuccessRateTimePoint[] {
  return meanSeries(groups, (point) =>
    Number.isFinite(point.success_rate)
      ? Math.min(100, Math.max(0, point.success_rate))
      : null
  ).map(({ timestamp, value }) => ({
    timestamp,
    success_rate: Math.round(value * 100) / 100,
  }))
}
