/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

import {
  bindTopupOrder,
  capturePreparedTopup,
  forgetTopup,
  prepareTopup,
  readPendingTopups,
  topupCloudStorageKey,
} from './topup-cloud-storage'

const dom = new Window({ url: 'https://example.test/wallet' })
Object.defineProperty(globalThis, 'window', { configurable: true, value: dom })
afterEach(() => dom.localStorage.clear())
after(() => dom.close())

test('each initiated checkout is bound before redirect without replacing another order', () => {
  const first = prepareTopup(7, 100, 10)
  assert.equal(capturePreparedTopup(7)?.attemptId, first.attemptId)
  assert.equal(capturePreparedTopup(8), null)
  const second = prepareTopup(7, 100, 200)
  bindTopupOrder(first, { success: true, data: { trade_no: 'first' } })
  bindTopupOrder(second, { message: 'success', data: { order_id: 'second' } })
  assert.deepEqual(
    readPendingTopups(7).map((x) => x.tradeNo),
    ['first', 'second']
  )
  forgetTopup(first)
  assert.deepEqual(
    readPendingTopups(7).map((x) => x.tradeNo),
    ['second']
  )
  assert.equal(capturePreparedTopup(7)?.attemptId, second.attemptId)
  forgetTopup(second)
  assert.equal(capturePreparedTopup(7), null)
})

test('legacy gateway order fields are accepted, failed or anonymous responses are not', () => {
  for (const response of [
    { success: true, trade_no: 'epay' },
    { message: 'success', data: { out_trade_no: 'epay' } },
    { message: 'success', data: { order_id: 'pancake' } },
  ]) {
    const intent = prepareTopup(7, 0, 10)
    bindTopupOrder(intent, response)
    assert.ok(
      readPendingTopups(7).find((x) => x.attemptId === intent.attemptId)
        ?.tradeNo
    )
    forgetTopup(intent)
  }
  for (const response of [
    null,
    { success: false, message: 'success', trade_no: 'bad' },
    { data: { trade_no: 'bad' } },
    { success: true, data: { token: 'not-an-order' } },
  ]) {
    const intent = prepareTopup(7, 0, 10)
    bindTopupOrder(intent, response)
    assert.equal(readPendingTopups(7).at(-1)?.tradeNo, undefined)
    forgetTopup(intent)
  }
})

test('independent tabs can restore a late order and cannot restore another account', () => {
  const now = Date.now()
  dom.localStorage.setItem(
    topupCloudStorageKey(7),
    JSON.stringify([
      {
        userId: 7,
        attemptId: 'late',
        tradeNo: 'late-order',
        launchedAt: now - 3_600_000,
        expiresAt: now + 60_000,
        beforeQuota: 100,
        expectedCredit: 10,
      },
      {
        userId: 7,
        attemptId: 'expired',
        expiresAt: now - 1,
        launchedAt: now - 100,
        beforeQuota: 0,
        expectedCredit: 10,
      },
      {
        userId: 8,
        attemptId: 'other',
        expiresAt: now + 100,
        launchedAt: now - 100,
        beforeQuota: 0,
        expectedCredit: 10,
      },
    ])
  )
  assert.deepEqual(
    readPendingTopups(7).map((x) => x.tradeNo),
    ['late-order']
  )
  assert.deepEqual(readPendingTopups(8), [])
})

test('a canceled attempt is never resurrected by a late gateway response', () => {
  const intent = prepareTopup(7, 0, 10)
  forgetTopup(intent)
  bindTopupOrder(intent, { success: true, trade_no: 'too-late' })
  assert.deepEqual(readPendingTopups(7), [])
})
