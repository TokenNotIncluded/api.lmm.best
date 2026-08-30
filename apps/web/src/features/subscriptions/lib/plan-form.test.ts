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

import type { SubscriptionPlan } from '../types'
import {
  PLAN_FORM_DEFAULTS,
  formValuesToPlanPayload,
  planToFormValues,
} from './plan-form'

describe('subscription plan fiat contract', () => {
  test('defaults new plans to explicit CNY fiat pricing and recurring Pancake products', () => {
    assert.equal(PLAN_FORM_DEFAULTS.currency, 'CNY')
    assert.equal(PLAN_FORM_DEFAULTS.waffo_pancake_product_type, 'subscription')
  })

  test('preserves explicit fiat currency through form and payload mapping', () => {
    const plan = {
      id: 1,
      title: 'Monthly',
      price_amount: 6.8,
      currency: 'CNY',
      duration_unit: 'month',
      duration_value: 1,
      waffo_pancake_product_type: 'one_time',
    } as SubscriptionPlan

    const values = planToFormValues(plan)
    assert.equal(values.price_amount, 6.8)
    assert.equal(values.currency, 'CNY')
    assert.equal(values.waffo_pancake_product_type, 'one_time')

    const payload = formValuesToPlanPayload(values)
    assert.equal(payload.plan.price_amount, 6.8)
    assert.equal(payload.plan.currency, 'CNY')
    assert.equal(payload.plan.waffo_pancake_product_type, 'one_time')
  })
})
