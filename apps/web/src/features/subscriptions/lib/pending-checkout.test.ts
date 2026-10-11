/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  SUBSCRIPTION_CHECKOUT_POLL_TIMEOUT_MS,
  applySubscriptionCheckoutEvidence,
  beginSubscriptionCheckoutConfirmation,
  restoreSubscriptionCheckouts,
  retrySubscriptionCheckoutConfirmation,
  shouldContinueSubscriptionCheckoutConfirmation,
  subscriptionCheckoutState,
  subscriptionCheckoutTradeNo,
  type SubscriptionCheckoutEvidence,
} from './pending-checkout.ts'

const reference = { tradeNo: 'order-A', userId: 1, planId: 3 }
function evidence(change: Partial<SubscriptionCheckoutEvidence> = {}): SubscriptionCheckoutEvidence {
  return { trade_no: 'order-A', user_id: 1, plan_id: 3, payment_status: 'pending',
    complete_time: 0, user_subscription_id: 0, confirmed: false, ...change }
}
function granted(change: Partial<SubscriptionCheckoutEvidence> = {}) {
  return evidence({ payment_status: 'success', complete_time: 2,
    user_subscription_id: 43, confirmed: true, ...change })
}

// Port of c288ed6c / #711. Keep the old-plan mutation and desired assertion;
// only the checkout input changes from a global fingerprint to an order ID.
describe('subscription checkout confirmation', () => {
  test('AUDIT-SUB-01: old-plan consumption cannot end a new-order confirmation', () => {
    const old = { id: 42, user_id: 1, plan_id: 3, status: 'active',
      start_time: 100, end_time: 1_000_000, amount_total: 1000,
      amount_used: 10, next_reset_time: 10_000 }
    const pending = beginSubscriptionCheckoutConfirmation(reference, 1000)
    old.amount_used = 11
    // A subscription-list refresh cannot be mistaken for order evidence.
    const listResponse = { all_subscriptions: [{ subscription: old }] }
    assert.equal(shouldContinueSubscriptionCheckoutConfirmation(
      pending, listResponse as unknown as SubscriptionCheckoutEvidence, 2000
    ), true, 'only usage changed; the pending purchase has not granted a subscription')
  })

  test('old reset, expiry, and a new grant on another device are not confirmation', () => {
    const pending = beginSubscriptionCheckoutConfirmation(reference, 1000)
    for (const change of [
      { amount_used: 0, next_reset_time: 20_000 },
      { status: 'expired', end_time: 1500 },
      { id: 99, user_id: 1, plan_id: 3, status: 'active' },
    ]) {
      assert.equal(shouldContinueSubscriptionCheckoutConfirmation(
        pending, change as unknown as SubscriptionCheckoutEvidence, 2000), true)
    }
    assert.equal(shouldContinueSubscriptionCheckoutConfirmation(pending,
      granted({ trade_no: 'other-device-order' }), 2000), true)
  })

  test('only this paid order with a matching grant can confirm', () => {
    const pending = beginSubscriptionCheckoutConfirmation(reference, 1000)
    for (const reply of [evidence(), granted({ confirmed: false }),
      granted({ user_subscription_id: 0 }), granted({ complete_time: 0 }),
      granted({ payment_status: 'pending' }), granted({ user_id: 2 }),
      granted({ plan_id: 4 }), granted({ trade_no: 'order-B' })]) {
      assert.equal(shouldContinueSubscriptionCheckoutConfirmation(pending, reply, 2000), true)
    }
    assert.equal(subscriptionCheckoutState(pending, granted(), 2000), 'confirmed')
    assert.equal(shouldContinueSubscriptionCheckoutConfirmation(pending, granted(), 2000), false)
  })

  test('two concurrent purchases remain independent with reversed response order', async () => {
    const a = beginSubscriptionCheckoutConfirmation(reference, 1000)
    const b = beginSubscriptionCheckoutConfirmation({ ...reference, tradeNo: 'order-B' }, 1001)
    let queue = [a, b]
    let resolveA!: (value: SubscriptionCheckoutEvidence) => void
    const waitA = new Promise<SubscriptionCheckoutEvidence>((resolve) => { resolveA = resolve })
    const requestA = waitA.then((reply) => { queue = applySubscriptionCheckoutEvidence(queue, a, reply, 2000) })
    queue = applySubscriptionCheckoutEvidence(queue, b, granted({ trade_no: 'order-B', user_subscription_id: 44 }), 2000)
    assert.deepEqual(queue.map((item) => item.state), ['pending', 'confirmed'])
    resolveA(granted())
    await requestA
    assert.deepEqual(queue.map((item) => item.state), ['confirmed', 'confirmed'])
    assert.strictEqual(applySubscriptionCheckoutEvidence(queue, a, granted(), 2001), queue)
  })

  test('reload retains each order, deadline and duplicate-confirmation tombstone', () => {
    const a = beginSubscriptionCheckoutConfirmation(reference, 1000)
    const b = beginSubscriptionCheckoutConfirmation({ ...reference, tradeNo: 'order-B' }, 1001)
    const stored = JSON.stringify({ version: 2, checkouts: [{ ...a, state: 'confirmed' }, b] })
    const restored = restoreSubscriptionCheckouts(stored, 1, 2000)
    assert.equal(restored.length, 2)
    assert.equal(restored[1].expiresAt, b.expiresAt)
    assert.strictEqual(applySubscriptionCheckoutEvidence(restored, restored[0], granted(), 2000), restored)
    assert.deepEqual(restoreSubscriptionCheckouts(stored, 2, 2000), [])
  })

  test('timeout is unknown and retains identity; late paid evidence can recover', () => {
    const a = beginSubscriptionCheckoutConfirmation(reference, 1000)
    const deadline = 1000 + SUBSCRIPTION_CHECKOUT_POLL_TIMEOUT_MS
    assert.equal(shouldContinueSubscriptionCheckoutConfirmation(a, undefined, deadline - 1), true)
    const queue = applySubscriptionCheckoutEvidence([a], a, undefined, deadline)
    assert.equal(queue[0].state, 'timed_out')
    assert.equal(queue[0].tradeNo, a.tradeNo)
    assert.equal(restoreSubscriptionCheckouts(JSON.stringify({ version: 2, checkouts: [a] }), 1, deadline)[0].state, 'timed_out')
    assert.equal(applySubscriptionCheckoutEvidence(queue, a, granted(), deadline + 1)[0].state, 'confirmed')
  })

  test('server cancellation/expiry/failure is not success; closing a dialog is not cancellation', () => {
    const a = beginSubscriptionCheckoutConfirmation(reference, 1000)
    assert.equal(subscriptionCheckoutState(a, undefined, 2000), 'pending')
    for (const status of ['cancelled', 'expired', 'failed', 'refunded']) {
      const queue = applySubscriptionCheckoutEvidence([a], a, evidence({ payment_status: status }), 2000)
      assert.notEqual(queue[0].state, 'confirmed')
      const retry = retrySubscriptionCheckoutConfirmation(queue[0], 3000)
      assert.equal(applySubscriptionCheckoutEvidence([retry], retry, granted(), 4000)[0].state, 'confirmed')
    }
  })

  test('a late response from a replaced attempt cannot stop a retry or another order', () => {
    const a = beginSubscriptionCheckoutConfirmation(reference, 1000)
    const retry = retrySubscriptionCheckoutConfirmation(a, 2000)
    const b = beginSubscriptionCheckoutConfirmation({ ...reference, tradeNo: 'order-B' }, 2000)
    const queue = [retry, b]
    assert.strictEqual(applySubscriptionCheckoutEvidence(queue, a, granted(), 3000), queue)
    assert.strictEqual(applySubscriptionCheckoutEvidence(queue, a, evidence({ payment_status: 'cancelled' }), 3000), queue)
    assert.equal(applySubscriptionCheckoutEvidence(queue, retry, granted(), 3000)[0].state, 'confirmed')
  })

  test('invalid, legacy, duplicate, and cross-user storage cannot invent an order', () => {
    const a = beginSubscriptionCheckoutConfirmation(reference, 1000)
    for (const stored of [null, 'not-json', 'null', '{"baseline":"42","expiresAt":999999}',
      JSON.stringify({ version: 2, checkouts: [{ ...a, userId: 2 }, { ...a, planId: 0 }, { ...a, attempt: 0 }] })]) {
      assert.deepEqual(restoreSubscriptionCheckouts(stored, 1, 2000), [])
    }
    assert.equal(restoreSubscriptionCheckouts(JSON.stringify({ version: 2, checkouts: [a, a] }), 1, 2000).length, 1)
    assert.throws(() => beginSubscriptionCheckoutConfirmation({ ...reference, tradeNo: '' }))
  })

  test('checkout identity comes from provider response data, not a payment URL', () => {
    assert.equal(subscriptionCheckoutTradeNo({ order_id: 'stripe-local-order' }), 'stripe-local-order')
    assert.equal(subscriptionCheckoutTradeNo({ order_id: 'pancake-local-order', session_id: 'not-the-order' }), 'pancake-local-order')
    assert.equal(subscriptionCheckoutTradeNo({ out_trade_no: 'epay-local-order' }, true), 'epay-local-order')
    assert.equal(subscriptionCheckoutTradeNo({ checkout_url: 'https://example.invalid/?order_id=untrusted' }), undefined)
    for (const value of ['', ' ', 'x'.repeat(256), 1, null]) {
      assert.equal(subscriptionCheckoutTradeNo({ order_id: value }), undefined)
    }
  })
})
