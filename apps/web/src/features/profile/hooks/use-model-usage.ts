/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'

import { useAuthStore } from '@/stores/auth-store'

import { getProfileUsageWindow } from '../api'
import {
  buildModelUsageQueryRanges,
  buildModelUsageReport,
  getModelUsageRange,
  type ModelUsageRangeKey,
  type ModelUsageReport,
} from '../lib/model-usage'

/**
 * Per-model usage for the share report.
 *
 * `/api/data/self` refuses a window wider than a month, so the range is split
 * into several bounded requests and folded back into one report. The window
 * count is small (at most 14 for a year) and the results are cached for five
 * minutes; callers should still treat an unresolved query as "not available"
 * rather than as zero.
 */
export function useModelUsage(
  rangeKey: ModelUsageRangeKey,
  accountCreatedTime?: number,
  enabled = true
) {
  const userId = useAuthStore((state) => state.auth.user?.id)
  const range = useMemo(
    () => getModelUsageRange(rangeKey, new Date(), accountCreatedTime),
    [rangeKey, accountCreatedTime]
  )
  const queryRanges = useMemo(() => buildModelUsageQueryRanges(range), [range])

  const query = useQuery({
    queryKey: [
      'profile',
      'model-usage',
      userId,
      rangeKey,
      range.start_timestamp,
      range.end_timestamp,
    ],
    queryFn: async () => {
      const windows = await Promise.all(
        queryRanges.map((window) => getProfileUsageWindow(window))
      )
      return windows.flat()
    },
    enabled: enabled && userId !== undefined,
    staleTime: 5 * 60 * 1000,
    retry: 1,
  })

  const report: ModelUsageReport = useMemo(
    () => buildModelUsageReport(query.data ?? []),
    [query.data]
  )

  return { ...query, range, rows: query.data ?? [], report }
}
