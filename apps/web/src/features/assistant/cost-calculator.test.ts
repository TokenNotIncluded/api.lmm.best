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

import type { PricingModel } from '@/features/pricing/types'

import { calculateAssistantTextCost } from './cost-calculator'

const model: PricingModel & {
  input_price: number
  output_price: number
  pricing_schema_version: number
  pricing_currency: string
} = {
  id: 1,
  model_name: 'example-model',
  quota_type: 0,
  model_ratio: 1.5,
  completion_ratio: 2,
  input_price: 3,
  output_price: 6,
  pricing_schema_version: 2,
  pricing_currency: 'USD',
  enable_groups: ['default'],
}

describe('assistant cost calculator', () => {
  test('uses published real USD prices and applies the account group ratio once', () => {
    const estimate = calculateAssistantTextCost(model, 0.8, 500_000, 250_000)
    assert.ok(estimate)
    assert.ok(Math.abs(estimate.inputRatePerMillionUSD - 2.4) < 1e-12)
    assert.ok(Math.abs(estimate.outputRatePerMillionUSD - 4.8) < 1e-12)
    assert.ok(Math.abs(estimate.totalUSD - 2.4) < 1e-12)
  })

  test('keeps canonical prices independent from legacy settlement ratios', () => {
    const estimate = calculateAssistantTextCost(
      { ...model, model_ratio: 7, completion_ratio: 9 },
      1,
      1_000_000,
      0
    )
    assert.equal(estimate?.totalUSD, 3)
  })

  test('requires versioned canonical USD rates instead of guessing the legacy unit', () => {
    const { input_price: _input, output_price: _output, ...legacyModel } = model
    assert.equal(
      calculateAssistantTextCost(legacyModel, 0.5, 1_000_000, 0),
      null
    )
    assert.equal(calculateAssistantTextCost(legacyModel, 1, 100, 100), null)
    assert.equal(
      calculateAssistantTextCost(
        { ...model, pricing_schema_version: 1 },
        1,
        100,
        100
      ),
      null
    )
    assert.equal(
      calculateAssistantTextCost(
        { ...model, pricing_currency: 'CNY' },
        1,
        100,
        100
      ),
      null
    )
  })

  test('rejects unsupported or invalid estimates', () => {
    assert.equal(
      calculateAssistantTextCost(
        { ...model, billing_mode: 'tiered_expr' },
        1,
        100,
        100
      ),
      null
    )
    assert.equal(calculateAssistantTextCost(model, 1, -1, 100), null)
    assert.equal(
      calculateAssistantTextCost({ ...model, output_price: -1 }, 1, 100, 100),
      null
    )
    assert.equal(calculateAssistantTextCost(model, 1e308, 1e308, 100), null)
  })
})
