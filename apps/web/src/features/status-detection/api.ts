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
import {
  getPerfMetrics,
  getPerfMetricsSummary,
} from '@/features/performance-metrics/api'
import type { PerfSummaryAllData } from '@/features/performance-metrics/types'

import type { ModelPerformanceSnapshot, StatusDetectionMetrics } from './types'

const MAX_CONCURRENCY = 6

export async function getStatusDetectionSummary(
  hours = 24
): Promise<PerfSummaryAllData> {
  const response = await getPerfMetricsSummary(hours)
  if (!response.success) {
    throw new Error(
      response.message?.trim() || 'Unable to load performance summary'
    )
  }
  return response
}

/** Fetches model details with a small worker pool so large catalogs do not
 * open one connection per model at once. A single unavailable model should
 * not hide status for every other model. */
export async function getStatusDetectionMetrics(
  modelNames: string[],
  hours = 24
): Promise<StatusDetectionMetrics> {
  const entries: ModelPerformanceSnapshot[] = []
  const failedModels: string[] = []
  let cursor = 0

  async function worker() {
    while (cursor < modelNames.length) {
      const index = cursor++
      const modelName = modelNames[index]
      if (!modelName) continue
      try {
        const response = await getPerfMetrics(modelName, hours)
        if (!response.success) {
          failedModels.push(modelName)
          continue
        }
        const groups = response.data.groups
        if (groups.length > 0) {
          entries.push({ modelName, groups })
        }
      } catch {
        failedModels.push(modelName)
      }
    }
  }

  const workers = Math.min(MAX_CONCURRENCY, modelNames.length)
  await Promise.all(Array.from({ length: workers }, () => worker()))

  if (modelNames.length > 0 && failedModels.length === modelNames.length) {
    throw new Error('Unable to load performance details')
  }

  return { entries, failedModels }
}
