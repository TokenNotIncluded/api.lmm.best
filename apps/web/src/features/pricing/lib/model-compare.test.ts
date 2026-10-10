/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

import type { PricingModel } from '../types'
import {
  MAX_COMPARE_MODELS,
  estimateWorkloadCost,
  getCompareTokenPrice,
  getSavingsPercent,
  pickBestIndexes,
  toggleCompareSelection,
} from './model-compare'

const model: PricingModel = {
  id: 1,
  model_name: 'test',
  pricing_schema_version: 2,
  pricing_currency: 'USD',
  input_price: 3,
  output_price: 15,
  cache_read_price: 0.3,
  quota_type: 0,
  model_ratio: 1.5,
  completion_ratio: 5,
  enable_groups: ['default', 'vip'],
  group_ratio: { default: 2, vip: 0.5 },
}

describe('compare selection', () => {
  it('toggles models and refuses to grow past the limit', () => {
    let selection: string[] = []
    for (let i = 0; i < MAX_COMPARE_MODELS + 2; i++) {
      selection = toggleCompareSelection(selection, `m${i}`)
    }
    assert.equal(selection.length, MAX_COMPARE_MODELS)
    assert.deepEqual(toggleCompareSelection(selection, 'm0'), [
      'm1',
      'm2',
      'm3',
    ])
    assert.deepEqual(toggleCompareSelection([], ''), [])
  })
})

describe('compare prices', () => {
  it('uses the same 1× base price for every group', () => {
    assert.equal(getCompareTokenPrice(model, 'input', 'M'), 3)
    assert.equal(getCompareTokenPrice(model, 'output', 'K', 'default'), 0.015)
  })
  it('does not invent token prices for request or dynamic billing', () => {
    assert.equal(
      getCompareTokenPrice({ ...model, quota_type: 1 }, 'input', 'M'),
      null
    )
    assert.equal(
      getCompareTokenPrice(
        { ...model, billing_mode: 'tiered_expr', billing_expr: 'p * 2' },
        'input',
        'M'
      ),
      null
    )
    assert.equal(
      getCompareTokenPrice({ ...model, cache_read_price: null }, 'cache', 'M'),
      null
    )
  })
  it('projects a monthly workload from the per-request estimate', () => {
    const cost = estimateWorkloadCost(model, {
      input: 10000,
      output: 2000,
      requestsPerDay: 100,
    })
    assert.ok(cost)
    assert.equal(cost.perRequest, 0.06)
    assert.equal(Math.round(cost.perMonth * 100) / 100, 180)
    assert.equal(
      estimateWorkloadCost(model, { input: 1, output: 1, requestsPerDay: -1 }),
      null
    )
  })
})

describe('compare highlights', () => {
  it('marks every tied winner but nothing when values are equal or alone', () => {
    assert.deepEqual([...pickBestIndexes([3, 1, null, 1], 'min')], [1, 3])
    assert.deepEqual([...pickBestIndexes([3, 1, 5], 'max')], [2])
    assert.equal(pickBestIndexes([2, 2], 'min').size, 0)
    assert.equal(pickBestIndexes([2, null, undefined], 'min').size, 0)
  })
  it('reports savings only for a real spread', () => {
    assert.equal(getSavingsPercent([4, 1, null]), 75)
    assert.equal(getSavingsPercent([1, 1]), null)
    assert.equal(getSavingsPercent([0, 0, 1]), 100)
    assert.equal(getSavingsPercent([1]), null)
  })
})
