import assert from 'node:assert/strict'
import { test } from 'node:test'

import { usesDedicatedPaymentPricing } from './payment-pricing'

test('built-in gateways cannot expose custom settlement pricing', () => {
  for (const type of ['stripe', 'waffo', 'waffo_pancake', 'alipay', 'wxpay']) {
    assert.equal(usesDedicatedPaymentPricing(type), true, type)
  }
  assert.equal(usesDedicatedPaymentPricing('custom_gateway'), false)
})
