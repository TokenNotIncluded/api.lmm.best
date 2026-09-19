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
import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { PerformanceGroup } from '@/features/performance-metrics/types'

import {
  getModelCatalogFailure,
  getRecentModelObservation,
} from './model-availability'

const now = 1800000000000
function groups(ts: number, rate: number): PerformanceGroup[] {
  return [
    {
      group: 'default',
      avg_ttft_ms: 0,
      avg_latency_ms: 0,
      avg_tps: 0,
      success_rate: rate,
      series: [
        {
          ts,
          success_rate: rate,
          avg_ttft_ms: 0,
          avg_latency_ms: 0,
          avg_tps: 0,
        },
      ],
    },
  ]
}
test('missing, stale, future and invalid observations do not imply availability', () => {
  for (const input of [
    [],
    groups(now / 1000 - 3601, 100),
    groups(now / 1000 + 1, 100),
    groups(now / 1000, Number.NaN),
  ]) {
    assert.equal(getRecentModelObservation(input, now).state, 'unknown')
  }
})
test('latest real samples distinguish observed success from failures', () => {
  assert.equal(
    getRecentModelObservation(groups(now / 1000 - 60, 100), now).state,
    'successful'
  )
  assert.equal(
    getRecentModelObservation(groups(now / 1000 - 60, 0), now).state,
    'failures'
  )
  assert.equal(
    getRecentModelObservation(
      [...groups(now / 1000 - 60, 100), ...groups(now / 1000 - 60, 50)],
      now
    ).state,
    'failures'
  )
})
test('permission errors are distinct from catalog failures', () => {
  assert.equal(getModelCatalogFailure(403), 'access')
  assert.equal(getModelCatalogFailure(401), 'access')
  assert.equal(getModelCatalogFailure(503), 'load')
})
