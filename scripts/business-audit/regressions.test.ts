/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
// Opt-in desired-behavior checks. Failure means the business defect remains.
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { consumeAssistantAISDKStream } from '../../apps/web/src/features/assistant/assistant-ai-stream.ts'
import {
  beginSubscriptionCheckoutConfirmation,
  shouldContinueSubscriptionCheckoutConfirmation,
  subscriptionCheckoutFingerprint,
} from '../../apps/web/src/features/subscriptions/lib/pending-checkout.ts'

function eventStream(text: string) {
  return new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(new TextEncoder().encode(text))
      controller.close()
    },
  })
}

test('a terminated assistant response resolves and releases its reader', async () => {
  const body = eventStream('event: done\ndata: {"content":"complete"}\n\n')
  assert.deepEqual(
    await consumeAssistantAISDKStream(body, {}, { timeoutMs: 1000 }),
    { content: 'complete' }
  )
  assert.equal(body.locked, false)
})

test('AUDIT-SSE-01: EOF cannot commit an unterminated success event', async () => {
  const body = eventStream('event: done\ndata: {"content":"incomplete-frame"}')
  try {
    await assert.rejects(
      consumeAssistantAISDKStream(body, {}, { timeoutMs: 1000 }),
      /ended before completion|invalid|incomplete/i
    )
  } finally {
    assert.equal(body.locked, false)
  }
})

function record(amountUsed: number, id = 42) {
  return {
    subscription: {
      id,
      user_id: 1,
      plan_id: 3,
      status: 'active',
      start_time: 100,
      end_time: 1_000_000,
      amount_total: 1000,
      amount_used: amountUsed,
      next_reset_time: 10_000,
    },
  }
}

test('AUDIT-SUB-01: old-plan consumption cannot end a new-order confirmation', () => {
  const before = subscriptionCheckoutFingerprint([record(10)])
  const pending = beginSubscriptionCheckoutConfirmation(before, 1000)
  const after = subscriptionCheckoutFingerprint([record(11)])
  assert.equal(
    shouldContinueSubscriptionCheckoutConfirmation(pending, after, 2000),
    true,
    'only usage changed; the pending purchase has not granted a subscription'
  )
})

test('a newly granted subscription changes the confirmation state', () => {
  const before = subscriptionCheckoutFingerprint([record(10)])
  const pending = beginSubscriptionCheckoutConfirmation(before, 1000)
  const after = subscriptionCheckoutFingerprint([record(10), record(0, 43)])
  assert.equal(
    shouldContinueSubscriptionCheckoutConfirmation(pending, after, 2000),
    false
  )
})
