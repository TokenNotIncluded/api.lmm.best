/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createReferralHistoryRequests } from './referral-history-requests'

function required<T>(value: T | null): T {
  assert.ok(value)
  return value
}

test('blocks a second request synchronously before React can render', () => {
  const requests = createReferralHistoryRequests()
  const first = required(requests.begin(0))
  assert.equal(requests.begin(0), null)
  assert.equal(requests.begin(50), null)
  assert.equal(requests.isCurrent(first), true)
  assert.equal(requests.finish(first), true)
  assert.equal(requests.isCurrent(first), false)
  assert.equal(required(requests.begin(50)).before, 50)
})

test('cancellation aborts transport and detaches ownership before callbacks', () => {
  const requests = createReferralHistoryRequests()
  const first = required(requests.begin(0))
  let detached = false
  first.controller.signal.addEventListener('abort', () => {
    detached = !requests.isCurrent(first)
    assert.equal(requests.finish(first), false)
  })
  requests.cancel()
  assert.equal(detached, true)
  assert.equal(first.controller.signal.aborted, true)
  assert.equal(requests.isCurrent(first), false)
})

test('an obsolete finally cannot release a reopened dialog request', () => {
  const requests = createReferralHistoryRequests()
  const first = required(requests.begin(0))
  requests.cancel()
  const reopened = required(requests.begin(0))
  assert.equal(requests.finish(first), false)
  assert.equal(requests.isCurrent(reopened), true)
  assert.equal(requests.begin(50), null)
  assert.equal(reopened.controller.signal.aborted, false)
})

test('failed pages can retry the same cursor with a fresh signal', () => {
  const requests = createReferralHistoryRequests()
  const failed = required(requests.begin(123))
  assert.equal(requests.finish(failed), true)
  const retry = required(requests.begin(failed.before))
  assert.equal(retry.before, 123)
  assert.notEqual(retry.controller, failed.controller)
  assert.equal(requests.isCurrent(failed), false)
})

test('repeated cleanup is harmless and instances do not cancel each other', () => {
  const firstDialog = createReferralHistoryRequests()
  const otherDialog = createReferralHistoryRequests()
  const first = required(firstDialog.begin(0))
  const other = required(otherDialog.begin(0))
  firstDialog.cancel()
  firstDialog.cancel()
  assert.equal(first.controller.signal.aborted, true)
  assert.equal(other.controller.signal.aborted, false)
  assert.equal(otherDialog.isCurrent(other), true)
})

test('abort callback cannot cause stale cleanup to clear a replacement', () => {
  const requests = createReferralHistoryRequests()
  const first = required(requests.begin(0))
  let replacement: ReturnType<typeof requests.begin> = null
  first.controller.signal.addEventListener('abort', () => {
    replacement = requests.begin(0)
  })
  requests.cancel()
  assert.equal(requests.finish(first), false)
  assert.equal(requests.isCurrent(required<typeof first>(replacement)), true)
})
