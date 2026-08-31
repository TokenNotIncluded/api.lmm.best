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
  ModelPerformanceSnapshot,
  StatusGroup,
  StatusSort,
} from '../types'

type GroupAccumulator = {
  models: Set<string>
  ttfts: number[]
  latencies: number[]
  throughputs: number[]
  successRates: number[]
  successTrendByTimestamp: Map<number, number[]>
  ttftTrendByTimestamp: Map<number, number[]>
}

function average(values: number[], predicate: (value: number) => boolean) {
  const valid = values.filter(predicate)
  if (valid.length === 0) return Number.NaN
  return valid.reduce((sum, value) => sum + value, 0) / valid.length
}

/**
 * Converts per-model performance responses into one stable row per group.
 * The API does not expose request counts for each group, so values are
 * intentionally averaged per model rather than presented as weighted totals.
 */
export function aggregateStatusGroups(
  entries: ModelPerformanceSnapshot[]
): StatusGroup[] {
  const groups = new Map<string, GroupAccumulator>()

  for (const entry of entries) {
    for (const group of entry.groups) {
      const name = group.group.trim()
      if (!name) continue
      const accumulator = groups.get(name) ?? {
        models: new Set<string>(),
        ttfts: [],
        latencies: [],
        throughputs: [],
        successRates: [],
        successTrendByTimestamp: new Map<number, number[]>(),
        ttftTrendByTimestamp: new Map<number, number[]>(),
      }

      accumulator.models.add(entry.modelName)
      if (Number.isFinite(group.avg_ttft_ms) && group.avg_ttft_ms > 0) {
        accumulator.ttfts.push(group.avg_ttft_ms)
      }
      if (Number.isFinite(group.avg_latency_ms) && group.avg_latency_ms > 0) {
        accumulator.latencies.push(group.avg_latency_ms)
      }
      if (Number.isFinite(group.avg_tps) && group.avg_tps > 0) {
        accumulator.throughputs.push(group.avg_tps)
      }
      if (Number.isFinite(group.success_rate)) {
        accumulator.successRates.push(group.success_rate)
      }
      for (const point of group.series ?? []) {
        if (!Number.isFinite(point.ts)) continue

        if (Number.isFinite(point.success_rate)) {
          const rates = accumulator.successTrendByTimestamp.get(point.ts) ?? []
          rates.push(point.success_rate)
          accumulator.successTrendByTimestamp.set(point.ts, rates)
        }

        if (Number.isFinite(point.avg_ttft_ms) && point.avg_ttft_ms > 0) {
          const ttfts = accumulator.ttftTrendByTimestamp.get(point.ts) ?? []
          ttfts.push(point.avg_ttft_ms)
          accumulator.ttftTrendByTimestamp.set(point.ts, ttfts)
        }
      }
      groups.set(name, accumulator)
    }
  }

  return [...groups.entries()]
    .map(([group, accumulator]) => ({
      group,
      avgTtftMs: average(accumulator.ttfts, (value) => value > 0),
      avgLatencyMs: average(accumulator.latencies, (value) => value > 0),
      avgTps: average(accumulator.throughputs, (value) => value > 0),
      successRate: average(accumulator.successRates, Number.isFinite),
      successTrend: [...accumulator.successTrendByTimestamp.entries()]
        .sort(([left], [right]) => left - right)
        .map(([, rates]) => average(rates, Number.isFinite)),
      ttftTrend: [...accumulator.ttftTrendByTimestamp.entries()]
        .sort(([left], [right]) => left - right)
        .map(([, ttfts]) => average(ttfts, (value) => value > 0)),
      modelCount: accumulator.models.size,
    }))
    .sort((left, right) => left.group.localeCompare(right.group))
}

function finitePositiveOrInfinity(value: number) {
  return Number.isFinite(value) && value > 0 ? value : Number.POSITIVE_INFINITY
}

export function sortStatusGroups(groups: StatusGroup[], sort: StatusSort) {
  return [...groups].sort((left, right) => {
    if (sort === 'name') return left.group.localeCompare(right.group)
    if (sort === 'reliability') {
      const successDelta = right.successRate - left.successRate
      if (successDelta !== 0) return successDelta
    }

    const ttftDelta =
      finitePositiveOrInfinity(left.avgTtftMs) -
      finitePositiveOrInfinity(right.avgTtftMs)
    if (ttftDelta !== 0) return ttftDelta
    return left.group.localeCompare(right.group)
  })
}
