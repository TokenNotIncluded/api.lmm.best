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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import type {
  PerformanceGroup,
  PerformanceSeriesPoint,
} from '@/features/performance-metrics/types'

import { toLatencySeries, toSuccessRateSeries } from './performance-series.ts'

function group(series: Partial<PerformanceSeriesPoint>[]): PerformanceGroup {
  return {
    group: 'default',
    avg_ttft_ms: 0,
    avg_latency_ms: 0,
    avg_tps: 0,
    success_rate: 0,
    series: series.map((point) => ({
      ts: 60,
      avg_ttft_ms: 0,
      avg_latency_ms: 0,
      avg_tps: 0,
      success_rate: Number.NaN,
      ...point,
    })),
  }
}

describe('model performance observations', () => {
  test('does not invent samples when the server has no observations', () => {
    assert.deepEqual(toLatencySeries([]), [])
    assert.deepEqual(toSuccessRateSeries([group([])]), [])
    assert.deepEqual(toSuccessRateSeries([group([{}])]), [])
  })

  test('sorts actual timestamps and averages only observed group latency', () => {
    assert.deepEqual(
      toLatencySeries([
        group([
          { ts: 120, avg_ttft_ms: 90 },
          { ts: 60, avg_ttft_ms: 100 },
        ]),
        group([
          { ts: 60, avg_ttft_ms: 201 },
          { ts: 90, avg_ttft_ms: 0 },
        ]),
      ]),
      [
        {
          timestamp: '1970-01-01T00:01:00.000Z',
          group: 'latency',
          ttft_ms: 151,
        },
        {
          timestamp: '1970-01-01T00:02:00.000Z',
          group: 'latency',
          ttft_ms: 90,
        },
      ]
    )
  })

  test('excludes non-positive and non-finite latency rather than plotting it', () => {
    assert.deepEqual(
      toLatencySeries([
        group([
          { avg_ttft_ms: -1 },
          { avg_ttft_ms: Number.NaN },
          { avg_ttft_ms: Number.POSITIVE_INFINITY },
        ]),
      ]),
      []
    )
  })

  test('keeps a real zero success rate and does not infer incidents or downtime', () => {
    assert.deepEqual(
      toSuccessRateSeries([
        group([
          { ts: 120, success_rate: 0 },
          { ts: 60, success_rate: 99 },
        ]),
        group([{ ts: 60, success_rate: 95 }, { ts: 90 }]),
      ]),
      [
        { timestamp: '1970-01-01T00:01:00.000Z', success_rate: 97 },
        { timestamp: '1970-01-01T00:02:00.000Z', success_rate: 0 },
      ]
    )
  })

  test('bounds reported percentages and omits non-finite values', () => {
    const points = toSuccessRateSeries([
      group([
        { ts: 0, success_rate: -10 },
        { ts: 60, success_rate: 110 },
        { ts: 120, success_rate: Number.POSITIVE_INFINITY },
      ]),
    ])
    assert.deepEqual(
      points.map((point) => point.success_rate),
      [0, 100]
    )
  })

  test('ignores invalid timestamps for both charts without throwing', () => {
    const invalid = group(
      [Number.NaN, Number.POSITIVE_INFINITY, 1e20].map((ts) => ({
        ts,
        avg_ttft_ms: 100,
        success_rate: 100,
      }))
    )
    assert.deepEqual(toLatencySeries([invalid]), [])
    assert.deepEqual(toSuccessRateSeries([invalid]), [])
  })
})
