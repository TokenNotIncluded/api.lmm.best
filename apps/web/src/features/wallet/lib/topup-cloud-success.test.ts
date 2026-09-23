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

const launchedAt = 1_800_000_000_000
const pending: PendingTopupCloud = {
  userId: 7,
  launchedAt,
  expiresAt: launchedAt + 900_000,
  baselineSuccessId: 10,
  beforeQuota: 5_000_000,
  expectedCredit: 10,
}

function record(
  id: number,
  status: TopupRecord['status'],
  createTime = launchedAt / 1000
): TopupRecord {
  return {
    id,
    user_id: 7,
    amount: 10,
    money: 10,
    trade_no: `order-${id}`,
    payment_method: 'waffo_pancake',
    create_time: createTime,
    complete_time: createTime + 2,
    status,
  }
}

test('a pending or expired checkout never starts the token success animation', () => {
  assert.equal(
    findConfirmedTopup(
      [record(12, 'pending'), record(11, 'expired'), record(10, 'success')],
      pending
    ),
    undefined
  )
})

test('a newly confirmed top-up starts the animation exactly for that order', () => {
  const success = record(13, 'success')
  assert.equal(findConfirmedTopup([success], pending), success)
})

test('a recent pre-existing success is rejected without a baseline ID', () => {
  assert.equal(
    findConfirmedTopup([record(13, 'success', launchedAt / 1000 - 10)], {
      ...pending,
      baselineSuccessId: 0,
    }),
    undefined
  )
})
