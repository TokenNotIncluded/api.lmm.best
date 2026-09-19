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
import type { PerformanceGroup } from '@/features/performance-metrics/types'

// Historical observations are evidence of requests, never a live health probe.
export function getRecentModelObservation(
  groups: PerformanceGroup[],
  observedAtMs: number
) {
  const points = groups
    .flatMap((group) => group.series ?? [])
    .filter(
      (point) =>
        Number.isFinite(point.ts) &&
        point.ts > 0 &&
        point.ts * 1000 <= observedAtMs &&
        Number.isFinite(point.success_rate) &&
        point.success_rate >= 0 &&
        point.success_rate <= 100
    )
  const latestAt = points.reduce(
    (latest, point) => Math.max(latest, point.ts),
    0
  )
  if (!latestAt || observedAtMs - latestAt * 1000 > 60 * 60 * 1000) {
    return { state: 'unknown' as const, latestAt: latestAt || null }
  }
  const recent = points.filter(
    (point) => observedAtMs - point.ts * 1000 <= 60 * 60 * 1000
  )
  return {
    state: recent.every((point) => point.success_rate === 100)
      ? ('successful' as const)
      : ('failures' as const),
    latestAt,
  }
}

export function getModelCatalogFailure(status?: number): 'access' | 'load' {
  return status === 401 || status === 403 ? 'access' : 'load'
}
