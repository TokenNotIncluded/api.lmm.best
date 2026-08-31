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
import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'

import { getPerfMetricsSummary } from '@/features/performance-metrics/api'
import type { PerfModelSummary } from '@/features/performance-metrics/types'

/** Same freshness as the model-details performance tab. */
const PERF_SUMMARY_STALE_TIME_MS = 60 * 1000
const PERF_SUMMARY_HOURS = 24
const PERF_SUMMARY_QUERY_KEY = [
  'perf-metrics-summary',
  PERF_SUMMARY_HOURS,
] as const

/**
 * Fold per-model perf summary rows into one entry per exact model name.
 * Exported for tests; the hook keeps this pure so upstream failures degrade
 * to an empty map instead of an error state.
 */
export function buildPerfMap(
  models: PerfModelSummary[] | undefined
): Map<string, PerfModelSummary> {
  const map = new Map<string, PerfModelSummary>()
  for (const model of models ?? []) {
    if (model.model_name.trim().length === 0) continue
    map.set(model.model_name, model)
  }
  return map
}

/**
 * Performance map for the Model Square: latency / throughput / status per
 * model from the public perf-metrics summary. When the endpoint is disabled,
 * empty, or failing, the map stays empty and callers render their "no data"
 * empty state — the summary route itself is already public under the pricing
 * nav module, so no extra auth handling is needed here.
 */
export function usePerfMap() {
  const { data, isLoading } = useQuery({
    queryKey: PERF_SUMMARY_QUERY_KEY,
    queryFn: () => getPerfMetricsSummary(PERF_SUMMARY_HOURS),
    staleTime: PERF_SUMMARY_STALE_TIME_MS,
    retry: false,
  })

  const perfMap = useMemo(
    () => buildPerfMap(data?.success ? data.data.models : undefined),
    [data]
  )

  return { perfMap, isLoading }
}
