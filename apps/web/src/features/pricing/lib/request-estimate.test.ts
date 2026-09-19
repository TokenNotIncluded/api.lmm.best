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
import { describe, it } from 'node:test'

import type { PricingModel } from '../types'
import { estimateRequestCost } from './request-estimate'

const model: PricingModel = {
  id: 1,
  model_name: 'test',
  quota_type: 0,
  model_ratio: 1.5,
  completion_ratio: 5,
  cache_ratio: 0.1,
  enable_groups: ['default'],
}
describe('request estimate', () => {
  it('applies group once and replaces cached input rather than adding it', () => {
    assert.equal(estimateRequestCost(model, 2, 10000, 2000, 0), 0.12)
    assert.equal(estimateRequestCost(model, 2, 10000, 2000, 4000), 0.0984)
    assert.equal(estimateRequestCost(model, 0, 10000, 2000, 0), 0)
  })
  it('preserves unknown pricing and refuses unsupported expressions', () => {
    assert.equal(estimateRequestCost(model, undefined, 1, 1, 0), null)
    assert.equal(
      estimateRequestCost(
        { ...model, billing_mode: 'tiered_expr' },
        1,
        1,
        1,
        0
      ),
      null
    )
    assert.equal(
      estimateRequestCost({ ...model, cache_ratio: null }, 1, 10, 1, 1),
      null
    )
    assert.equal(estimateRequestCost(model, 1, 10, 1, 11), null)
    assert.equal(estimateRequestCost(model, 1, Number.NaN, 1, 0), null)
  })
  it('supports per-request prices without fabricating missing prices', () => {
    assert.equal(
      estimateRequestCost(
        { ...model, quota_type: 1, model_price: 0.4 },
        2,
        10000,
        2000,
        0
      ),
      0.8
    )
    assert.equal(
      estimateRequestCost({ ...model, quota_type: 1 }, 1, 0, 0, 0),
      null
    )
  })
})

describe('expression request estimates', () => {
  const dynamic = (billing_expr: string): PricingModel => ({
    ...model,
    billing_mode: 'tiered_expr',
    billing_expr,
  })
  it('selects tiers by total context even when most tokens are cached', () => {
    const tiered = dynamic(
      'v1:len <= 200000 ? tier("short", p * 2 + cr * 0.2 + c * 8) : tier("long", p * 4 + cr * 0.4 + c * 16)'
    )
    assert.equal(estimateRequestCost(tiered, 2, 300000, 1000, 250000), 0.632)
    assert.equal(estimateRequestCost(tiered, 1, 200000, 1000, 0), 0.408)
    assert.equal(estimateRequestCost(tiered, 1, 200001, 1000, 0), 0.816004)
  })
  it('does not subtract cache when the expression does not price it separately', () => {
    assert.equal(
      estimateRequestCost(dynamic('p * 2 + c * 8'), 1, 10000, 1000, 9000),
      0.028
    )
    assert.equal(
      estimateRequestCost(
        dynamic('p * 2 + cr * 0.2 + c * 8'),
        1,
        10000,
        1000,
        9000
      ),
      0.0118
    )
  })
  it('inspects all branches for request context and unsupported variables', () => {
    for (const expression of [
      'p < 20000 ? p * 2 : header("x-price")',
      'p < 20000 ? p * 2 : hour("UTC")',
      'p < 20000 ? p * 2 : unknown',
      'v2:p * 2',
      'p / 0',
      '-p',
      'globalThis.alert(1)',
    ]) {
      assert.equal(
        estimateRequestCost(dynamic(expression), 1, 10000, 1000, 0),
        null,
        expression
      )
    }
  })
})
