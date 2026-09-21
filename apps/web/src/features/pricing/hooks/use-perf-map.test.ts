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
import { describe, test } from 'node:test'

import type { PerfModelSummary } from '@/features/performance-metrics/types'

import { buildPerfMap } from './use-perf-map'

describe('buildPerfMap', () => {
  test('indexes a complete summary by its exact model name without losing data', () => {
    const summary: PerfModelSummary = {
      model_name: 'OpenAI/GPT-4o',
      avg_latency_ms: 428.5,
      success_rate: 99.75,
      avg_tps: 67.25,
      recent_success_rates: [98, 99, 100],
      request_count: 42,
    }

    const perfMap = buildPerfMap([summary])

    assert.equal(perfMap.get('OpenAI/GPT-4o'), summary)
    assert.equal(perfMap.has('openai/gpt-4o'), false)
  })

  test('skips summaries without a usable model name', () => {
    const unnamedSummary: PerfModelSummary = {
      model_name: '',
      avg_latency_ms: 500,
      success_rate: 100,
      avg_tps: 80,
    }

    const blankSummary = { ...unnamedSummary, model_name: '   ' }
    const perfMap = buildPerfMap([unnamedSummary, blankSummary])

    assert.equal(perfMap.size, 0)
  })

  test('returns an empty map when summary data is unavailable', () => {
    const perfMap = buildPerfMap(undefined)

    assert.equal(perfMap.size, 0)
  })
})
