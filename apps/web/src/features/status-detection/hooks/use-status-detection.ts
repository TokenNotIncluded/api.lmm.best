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
import { useQuery } from '@tanstack/react-query'
import { useCallback, useMemo } from 'react'

import { getStatusDetectionMetrics, getStatusDetectionSummary } from '../api'
import { aggregateStatusGroups } from '../lib/aggregate'

const WINDOW_HOURS = 24
const MAX_MODELS = 48

export function useStatusDetection() {
  const summaryQuery = useQuery({
    queryKey: ['perf-metrics-summary', WINDOW_HOURS],
    queryFn: () => getStatusDetectionSummary(WINDOW_HOURS),
    staleTime: 60 * 1000,
    retry: false,
  })

  const modelNames = useMemo(
    () =>
      (summaryQuery.data?.data.models ?? [])
        .map((model) => model.model_name.trim())
        .filter(Boolean)
        .slice(0, MAX_MODELS),
    [summaryQuery.data]
  )

  const detailsQuery = useQuery({
    queryKey: ['status-detection-metrics', WINDOW_HOURS, modelNames],
    queryFn: () => getStatusDetectionMetrics(modelNames, WINDOW_HOURS),
    enabled: modelNames.length > 0,
    staleTime: 60 * 1000,
    retry: false,
  })

  const groups = useMemo(
    () => aggregateStatusGroups(detailsQuery.data?.entries ?? []),
    [detailsQuery.data?.entries]
  )

  const refresh = useCallback(async () => {
    await summaryQuery.refetch()
    await detailsQuery.refetch()
  }, [detailsQuery, summaryQuery])

  return {
    groups,
    modelCount: modelNames.length,
    modelsWithData: detailsQuery.data?.entries.length ?? 0,
    failedModelCount: detailsQuery.data?.failedModels.length ?? 0,
    isLoading: summaryQuery.isLoading || detailsQuery.isLoading,
    isFetching: summaryQuery.isFetching || detailsQuery.isFetching,
    error: summaryQuery.error ?? detailsQuery.error,
    hasSummary: Boolean(summaryQuery.data?.success),
    refresh,
  }
}
