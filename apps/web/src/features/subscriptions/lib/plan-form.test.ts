import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import type { SubscriptionPlan } from '../types'
import {
  PLAN_FORM_DEFAULTS,
  formValuesToPlanPayload,
  planToFormValues,
} from './plan-form'

describe('subscription plan fiat contract', () => {
  test('defaults new plans to explicit CNY fiat pricing', () => {
    assert.equal(PLAN_FORM_DEFAULTS.currency, 'CNY')
  })

  test('preserves explicit fiat currency through form and payload mapping', () => {
    const plan = {
      id: 1,
      title: 'Monthly',
      price_amount: 6.8,
      currency: 'CNY',
      duration_unit: 'month',
      duration_value: 1,
    } as SubscriptionPlan

    const values = planToFormValues(plan)
    assert.equal(values.price_amount, 6.8)
    assert.equal(values.currency, 'CNY')

    const payload = formValuesToPlanPayload(values)
    assert.equal(payload.plan.price_amount, 6.8)
    assert.equal(payload.plan.currency, 'CNY')
  })
})
