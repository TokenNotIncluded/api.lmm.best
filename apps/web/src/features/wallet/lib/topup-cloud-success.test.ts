/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { TopupRecord } from '../types'
import {
  findConfirmedTopup,
  type PendingTopupCloud,
} from './topup-cloud-success'

const pending: PendingTopupCloud = {
  userId: 7,
  launchedAt: 1_800_000_000_000,
  expiresAt: 1_800_086_400_000,
  beforeQuota: 5_000_000,
  expectedCredit: 10,
  attemptId: 'attempt',
  tradeNo: 'my-order',
}
const record: TopupRecord = {
  id: 13,
  user_id: 7,
  amount: 10,
  money: 10,
  trade_no: 'my-order',
  payment_method: 'waffo_pancake',
  create_time: pending.launchedAt / 1000,
  complete_time: pending.launchedAt / 1000 + 1200,
  status: 'success',
}

test('only the exact server order and account can confirm checkout', () => {
  assert.equal(findConfirmedTopup([record], pending), record)
  for (const patch of [
    { trade_no: 'other-order' },
    { trade_no: 'other-order', amount: 200, id: 2000 },
    { user_id: 8 },
    { status: 'pending' as const },
    { status: 'expired' as const },
    { status: 'failed' as const },
  ]) {
    assert.equal(
      findConfirmedTopup([{ ...record, ...patch }], pending),
      undefined
    )
  }
  assert.equal(
    findConfirmedTopup([record], {
      ...pending,
      tradeNo: undefined,
      baselineSuccessId: 10,
    }),
    undefined
  )
})

test('late confirmation and changing list positions do not change order identity', () => {
  const other = { ...record, id: 500, trade_no: 'other-order', amount: 200 }
  assert.equal(findConfirmedTopup([other, record], pending), record)
  assert.equal(
    findConfirmedTopup([record], { ...pending, launchedAt: Date.now() }),
    record
  )
})
