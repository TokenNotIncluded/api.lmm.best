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

import type { PlanRecord } from '../types'
import {
  getAdminPlanPaymentMethods,
  isPlanBalancePaymentAvailable,
} from './payment-methods'

function planRecord(overrides: Partial<PlanRecord> = {}): PlanRecord {
  return {
    plan: {
      id: 1,
      title: 'Plan',
      price_amount: 10,
      currency: 'USD',
      duration_unit: 'month',
      duration_value: 1,
      quota_reset_period: 'never',
      enabled: true,
      sort_order: 0,
      allow_balance_pay: true,
      allow_wallet_overflow: true,
      max_purchase_per_user: 0,
      total_amount: 0,
    },
    ...overrides,
  }
}

describe('subscription payment catalog', () => {
  test('treats an empty server catalog as authoritative', () => {
    const record = planRecord({ payment_methods: [] })

    assert.deepEqual(getAdminPlanPaymentMethods(record), [])
    assert.equal(isPlanBalancePaymentAvailable(record), false)
  })

  test('falls back to legacy plan fields only when the catalog is omitted', () => {
    const record = planRecord({
      plan: {
        ...planRecord().plan,
        stripe_price_id: 'price_plan',
        waffo_pancake_product_id: 'PROD_plan',
      },
    })

    assert.deepEqual(getAdminPlanPaymentMethods(record), [
      'balance',
      'stripe',
      'waffo_pancake',
    ])
    assert.equal(isPlanBalancePaymentAvailable(record), true)
  })
})
