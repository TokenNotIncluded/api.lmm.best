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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { summarizeAssistantUsage } from './usage-summary'

describe('summarizeAssistantUsage', () => {
  test('aggregates totals and ranks duplicate model rows by spend', () => {
    const summary = summarizeAssistantUsage([
      {
        created_at: 1,
        model_name: 'model-a',
        count: 2,
        token_used: 50,
        quota: 2_000_000,
      },
      {
        created_at: 2,
        model_name: 'model-b',
        count: 4,
        token_used: 100,
        quota: 5_000_000,
      },
      {
        created_at: 3,
        model_name: 'model-a',
        count: 1,
        token_used: 25,
        quota: 1_000_000,
      },
    ])

    assert.equal(summary.requests, 7)
    assert.equal(summary.tokens, 175)
    assert.equal(summary.credits, 8_000_000)
    assert.deepEqual(
      summary.models.map((model) => ({
        model: model.model,
        requests: model.requests,
        credits: model.credits,
      })),
      [
        { model: 'model-b', requests: 4, credits: 5_000_000 },
        { model: 'model-a', requests: 3, credits: 3_000_000 },
      ]
    )
    assert.equal(summary.models[0]?.sharePercent, 62.5)
  })

  test('ignores invalid negative counters and falls back to token share', () => {
    const summary = summarizeAssistantUsage([
      {
        created_at: 1,
        model_name: 'model-a',
        count: -1,
        token_used: 30,
        quota: Number.NaN,
      },
      {
        created_at: 2,
        model_name: 'model-b',
        count: 2,
        token_used: 70,
        quota: -10,
      },
    ])

    assert.equal(summary.requests, 2)
    assert.equal(summary.tokens, 100)
    assert.equal(summary.credits, 0)
    assert.equal(summary.models[0]?.model, 'model-b')
    assert.equal(summary.models[0]?.sharePercent, 70)
  })
})
