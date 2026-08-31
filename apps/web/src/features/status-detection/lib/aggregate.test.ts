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

import { aggregateStatusGroups, sortStatusGroups } from './aggregate'

describe('aggregateStatusGroups', () => {
  test('averages model group metrics and aligns trend buckets', () => {
    const groups = aggregateStatusGroups([
      {
        modelName: 'alpha',
        groups: [
          {
            group: 'paid',
            avg_ttft_ms: 100,
            avg_latency_ms: 200,
            avg_tps: 10,
            success_rate: 100,
            series: [
              {
                ts: 2,
                avg_ttft_ms: 300,
                avg_latency_ms: 0,
                avg_tps: 0,
                success_rate: 80,
              },
              {
                ts: 1,
                avg_ttft_ms: 100,
                avg_latency_ms: 0,
                avg_tps: 0,
                success_rate: 100,
              },
            ],
          },
        ],
      },
      {
        modelName: 'beta',
        groups: [
          {
            group: 'paid',
            avg_ttft_ms: 100,
            avg_latency_ms: 400,
            avg_tps: 20,
            success_rate: 80,
            series: [
              {
                ts: 2,
                avg_ttft_ms: 500,
                avg_latency_ms: 0,
                avg_tps: 0,
                success_rate: 60,
              },
            ],
          },
        ],
      },
    ])

    assert.equal(groups.length, 1)
    assert.deepEqual(groups[0], {
      group: 'paid',
      avgTtftMs: 100,
      avgLatencyMs: 300,
      avgTps: 15,
      successRate: 90,
      successTrend: [100, 70],
      ttftTrend: [100, 400],
      modelCount: 2,
    })
  })

  test('ignores empty group names and invalid metric values', () => {
    const groups = aggregateStatusGroups([
      {
        modelName: 'alpha',
        groups: [
          {
            group: ' ',
            avg_ttft_ms: 0,
            avg_latency_ms: Number.NaN,
            avg_tps: Number.NaN,
            success_rate: Number.NaN,
            series: [],
          },
        ],
      },
    ])

    assert.deepEqual(groups, [])
  })

  test('sorts groups by first-token latency with missing data last', () => {
    const base = {
      avgLatencyMs: 0,
      avgTps: 0,
      successTrend: [],
      ttftTrend: [],
      modelCount: 1,
    }
    const groups = [
      { ...base, group: 'slow', avgTtftMs: 2_000, successRate: 99 },
      { ...base, group: 'unknown', avgTtftMs: Number.NaN, successRate: 100 },
      { ...base, group: 'fast', avgTtftMs: 250, successRate: 98 },
    ]

    assert.deepEqual(
      sortStatusGroups(groups, 'ttft').map((group) => group.group),
      ['fast', 'slow', 'unknown']
    )
    assert.deepEqual(
      sortStatusGroups(groups, 'reliability').map((group) => group.group),
      ['unknown', 'slow', 'fast']
    )
    assert.deepEqual(
      sortStatusGroups(groups, 'name').map((group) => group.group),
      ['fast', 'slow', 'unknown']
    )
  })
})
