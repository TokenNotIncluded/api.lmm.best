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

const MAX_MODELS = 48

type StatusDetectionOptions = {
  hours?: number
  model?: string
  group?: string
  vendor?: string
  modelVendors?: ReadonlyMap<string, string>
}

export function useStatusDetection(options: StatusDetectionOptions = {}) {
  const hours = options.hours ?? 24
  const modelFilter = options.model?.trim() ?? ''
  const groupFilter = options.group?.trim() ?? ''
  const vendorFilter = options.vendor?.trim() ?? ''
  const summaryQuery = useQuery({
    queryKey: ['perf-metrics-summary', hours],
    queryFn: () => getStatusDetectionSummary(hours),
    staleTime: 60 * 1000,
    retry: false,
  })

  const allModelNames = useMemo(
    () =>
      (summaryQuery.data?.data.models ?? [])
        .map((model) => model.model_name.trim())
        .filter(Boolean),
    [summaryQuery.data]
  )
  const modelNames = useMemo(
    () =>
      allModelNames
        .filter((modelName) => {
          if (modelFilter && modelName !== modelFilter) return false
          if (
            vendorFilter &&
            options.modelVendors?.get(modelName) !== vendorFilter
          ) {
            return false
          }
          return true
        })
        .slice(0, MAX_MODELS),
    [allModelNames, modelFilter, options.modelVendors, vendorFilter]
  )

  const detailsQuery = useQuery({
    queryKey: ['status-detection-metrics', hours, modelNames],
    queryFn: () => getStatusDetectionMetrics(modelNames, hours),
    enabled: modelNames.length > 0,
    staleTime: 60 * 1000,
    retry: false,
  })

  const allGroups = useMemo(
    () => aggregateStatusGroups(detailsQuery.data?.entries ?? []),
    [detailsQuery.data?.entries]
  )
  const groups = useMemo(
    () =>
      groupFilter
        ? allGroups.filter((group) => group.group === groupFilter)
        : allGroups,
    [allGroups, groupFilter]
  )
  const latestTimestamp = useMemo(() => {
    let latest = Number.NaN
    for (const entry of detailsQuery.data?.entries ?? []) {
      for (const group of entry.groups) {
        for (const point of group.series ?? []) {
          if (Number.isFinite(point.ts)) latest = Math.max(latest, point.ts)
        }
      }
    }
    return Number.isFinite(latest) ? latest : null
  }, [detailsQuery.data?.entries])

  const refresh = useCallback(async () => {
    await summaryQuery.refetch()
    await detailsQuery.refetch()
  }, [detailsQuery, summaryQuery])

  return {
    groups,
    availableGroups: allGroups.map((group) => group.group),
    availableModels: allModelNames,
    modelCount: modelNames.length,
    modelsWithData: detailsQuery.data?.entries.length ?? 0,
    failedModelCount: detailsQuery.data?.failedModels.length ?? 0,
    isLoading: summaryQuery.isLoading || detailsQuery.isLoading,
    isFetching: summaryQuery.isFetching || detailsQuery.isFetching,
    error: summaryQuery.error ?? detailsQuery.error,
    hasSummary: Boolean(summaryQuery.data?.success),
    latestTimestamp,
    hours,
    refresh,
  }
}
