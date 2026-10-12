/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  exactCredit,
  nativeCreditDisplay,
  walletProjection,
  type WalletProjectionUser,
} from './wallet-projection'

const native: WalletProjectionUser = {
  financial_projection_available: true,
  usage_projection_available: false,
  wallet_unit: 'core_credit',
  wallet_money_basis: 'unknown',
  wallet_money_available: false,
  quota: 199671,
  wallet_spendable_quota: 199671,
  wallet_reserved_quota: 280,
  wallet_debt: 0,
}

test('wallet known without imported usage counters: reserved is not available', () => {
  const value = walletProjection(native)
  assert.equal(value.available, 199671)
  assert.equal(value.reserved, 280)
  assert.equal(value.history, false)
  assert.equal(value.requests, null)
  assert.equal(value.readOnly, true)
})
test('signed available remains negative, debt is separate from spendable', () => {
  const value = walletProjection({
    ...native,
    quota: -70,
    wallet_spendable_quota: 0,
    wallet_debt: 70,
  })
  assert.equal(value.available, -70)
  assert.equal(value.spendable, 0)
  assert.equal(value.debt, 70)
  assert.match(
    nativeCreditDisplay(value.available, 'Credits', 'Unknown'),
    /-70 Credits/
  )
})
test('real zero is distinct from absent quota and absent buckets', () => {
  assert.equal(walletProjection({ ...native, quota: 0 }).available, 0)
  const absent = walletProjection({
    ...native,
    quota: undefined,
    wallet_reserved_quota: undefined,
  })
  assert.equal(absent.available, null)
  assert.equal(absent.reserved, null)
  assert.equal(nativeCreditDisplay(null, 'Credits', 'Unknown'), 'Unknown')
})
test('safe integer boundaries fail closed without rounding', () => {
  for (const quota of [Number.MAX_SAFE_INTEGER, -Number.MAX_SAFE_INTEGER]) {
    assert.equal(walletProjection({ ...native, quota }).available, quota)
  }
  for (const quota of [
    Number.MAX_SAFE_INTEGER + 1,
    -Number.MAX_SAFE_INTEGER - 1,
    Infinity,
    Number.NaN,
    0.5,
  ]) {
    assert.equal(walletProjection({ ...native, quota }).available, null)
  }
  assert.equal(exactCredit('42'), null)
  assert.equal(walletProjection({ ...native, wallet_debt: -1 }).debt, null)
})
test('missing FX basis does not change native units or authorize writes', () => {
  for (const wallet_money_basis of [undefined, 'unknown', 'legacy']) {
    const value = walletProjection({ ...native, wallet_money_basis })
    assert.equal(value.native, true)
    assert.equal(value.available, 199671)
    assert.equal(value.readOnly, true)
  }
})
test('known zero requests needs explicit counter with native history', () => {
  assert.equal(
    walletProjection({
      ...native,
      usage_projection_available: true,
      request_count: 0,
    }).requests,
    0
  )
  assert.equal(
    walletProjection({ ...native, usage_projection_available: true }).requests,
    null
  )
  assert.equal(walletProjection({ ...native, request_count: 0 }).requests, null)
})
test('unavailable projection ignores even populated financial fields', () => {
  const value = walletProjection({
    ...native,
    financial_projection_available: false,
  })
  assert.equal(value.available, null)
  assert.equal(value.reserved, null)
  assert.equal(value.debt, null)
})
test('legacy DTO with no new flags retains legacy fallback and write routing', () => {
  const value = walletProjection({ quota: 500000, request_count: 0 })
  assert.equal(value.native, false)
  assert.equal(value.available, 500000)
  assert.equal(value.history, true)
  assert.equal(value.requests, 0)
  assert.equal(value.readOnly, false)
  assert.equal(walletProjection({}).available, 0)
})
