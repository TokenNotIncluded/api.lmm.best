/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'

import {
  applySubscriptionCheckoutEvidence,
  beginSubscriptionCheckoutConfirmation,
  retrySubscriptionCheckoutConfirmation,
  type SubscriptionCheckoutEvidence,
} from '../../apps/web/src/features/subscriptions/lib/pending-checkout.ts'

// Query-level integration: use the real SQL from Go, not a second hand-written
// SELECT. SQLite settlement is simulated; this is not an HTTP/provider test.
test('database evidence drives only its own frontend checkout state', () => {
  const run = spawnSync('python3', [fileURLToPath(new URL('./subscription-confirmation-db.py', import.meta.url))], { encoding: 'utf8' })
  assert.equal(run.status, 0, run.stderr)
  const result = JSON.parse(run.stdout)
  const timeline = result.timeline as Record<string, SubscriptionCheckoutEvidence>
  const a = beginSubscriptionCheckoutConfirmation({ tradeNo: 'order-A', userId: 1, planId: 3 }, 1000)
  const b = beginSubscriptionCheckoutConfirmation({ tradeNo: 'order-B', userId: 1, planId: 3 }, 1000)
  let queue = [a, b]
  for (const name of ['pending', 'old_consumption', 'old_reset', 'old_expiry', 'a_after_b_paid', 'paid_without_grant']) {
    queue = applySubscriptionCheckoutEvidence(queue, a, timeline[name], 2000)
    assert.equal(queue[0].state, 'pending', name)
  }
  queue = applySubscriptionCheckoutEvidence(queue, a, timeline.other_device_paid, 2000)
  assert.equal(queue[0].state, 'pending')
  queue = applySubscriptionCheckoutEvidence(queue, b, timeline.other_device_paid, 2000)
  assert.deepEqual(queue.map((item) => item.state), ['pending', 'confirmed'])
  queue = applySubscriptionCheckoutEvidence(queue, a, timeline.cancelled, 2000)
  assert.equal(queue[0].state, 'cancelled')
  const retried = retrySubscriptionCheckoutConfirmation(queue[0], 3000)
  queue = [retried, queue[1]]
  queue = applySubscriptionCheckoutEvidence(queue, a, timeline.late_own_grant, 4000)
  assert.equal(queue[0].state, 'pending', 'old attempt cannot complete a newer one')
  queue = applySubscriptionCheckoutEvidence(queue, retried, timeline.late_own_grant, 4000)
  assert.deepEqual(queue.map((item) => item.state), ['confirmed', 'confirmed'])
  assert.equal(result.records.subscription_payment_events.length, 4)
  assert.equal(result.records.user_subscriptions.length, 7)
  assert.deepEqual(result.records.users.map((user: { quota: number }) => user.quota), [5000, 5000])
})

test('checkout wiring records the returned ID before every navigation', () => {
  const source = readFileSync(new URL('../../apps/web/src/features/subscriptions/components/dialogs/subscription-purchase-dialog.tsx', import.meta.url), 'utf8')
  for (const [method, next, navigation] of [
    ['Stripe', 'Creem', 'redirectToPaymentCheckout(checkout,'],
    ['Creem', 'WaffoPancake', 'redirectToPaymentCheckout(checkout,'],
    ['WaffoPancake', 'Epay', 'redirectCurrentWindowToPaymentCheckout(res.data.checkout_url)'],
    ['Epay', 'Balance', 'submitPaymentForm(res.url,'],
  ]) {
    const part = source.slice(source.indexOf(`const handlePay${method} =`), source.indexOf(`const handlePay${next} =`))
    const marker = part.indexOf('props.onCheckoutStarted?.(tradeNo, plan.id)')
    assert.ok(marker >= 0 && marker < part.indexOf(navigation), method)
    assert.ok(part.includes('&& tradeNo)'), `${method} requires an order ID`)
  }
  const card = readFileSync(new URL('../../apps/web/src/features/wallet/components/subscription-plans-card.tsx', import.meta.url), 'utf8')
  assert.ok(!card.includes('subscriptionCheckoutFingerprint'))
  assert.ok(card.includes('onCheckoutStarted={markCheckoutPending}'))
  const controller = readFileSync(new URL('../../apps/api-go/controller/subscription.go', import.meta.url), 'utf8')
  assert.ok(controller.includes('getSubscriptionCheckoutConfirmation(c)'))
})
