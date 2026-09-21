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
import type { PerformanceGroup } from '@/features/performance-metrics/types'

export type ModelPerformanceSnapshot = {
  modelName: string
  groups: PerformanceGroup[]
}

export type StatusGroup = {
  group: string
  avgTtftMs: number
  avgLatencyMs: number
  avgTps: number
  successRate: number
  successTrend: number[]
  ttftTrend: number[]
  modelCount: number
}

export type StatusSort = 'ttft' | 'reliability' | 'name'

export type StatusDetectionMetrics = {
  entries: ModelPerformanceSnapshot[]
  failedModels: string[]
}
