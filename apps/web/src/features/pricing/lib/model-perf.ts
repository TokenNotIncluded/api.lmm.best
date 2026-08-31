/*
Copyright (C) 2026 LIghtJUNction

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
*/
import {
  formatLatency,
  formatThroughput,
} from '@/features/performance-metrics/lib/format'
import type { PerfModelSummary } from '@/features/performance-metrics/types'

export type ModelPerfBadgeData = Pick<
  PerfModelSummary,
  'avg_latency_ms' | 'success_rate' | 'avg_tps' | 'recent_success_rates'
>

export type ModelPerfDisplay = {
  latency: string
  throughput: string
  successRate: string
  statusBars: Array<number | null>
}

export function getModelPerfDisplay(
  perf: ModelPerfBadgeData | undefined
): ModelPerfDisplay {
  let statusRates: number[] = []
  const recentRates =
    perf?.recent_success_rates?.filter((rate) => Number.isFinite(rate)) ?? []

  if (recentRates.length > 0) {
    statusRates = recentRates.slice(-3)
  } else if (perf && Number.isFinite(perf.success_rate)) {
    statusRates = [perf.success_rate]
  }

  return {
    latency: formatLatency(perf?.avg_latency_ms ?? Number.NaN),
    throughput: formatThroughput(perf?.avg_tps ?? Number.NaN),
    successRate:
      perf && Number.isFinite(perf.success_rate)
        ? `${perf.success_rate.toFixed(1)}%`
        : '—',
    statusBars: [
      ...Array(Math.max(0, 3 - statusRates.length)).fill(null),
      ...statusRates,
    ].slice(-3),
  }
}
