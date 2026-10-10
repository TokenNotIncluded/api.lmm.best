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
  formatCumulativeUserUsage,
  normalizedUserUsage,
} from './cumulative-user-usage'

const raw = 2491782362
const baseline = 371317138
const delta = 109575

test('canonical cumulative usage values the server baseline plus new raw credit delta once', () => {
  const user = {
    used_quota: raw,
    normalized_used_quota: baseline + delta,
    usage_projection_available: true,
  }
  const before = { ...user }
  const calls: number[] = []
  const formatted = formatCumulativeUserUsage(
    user,
    (quota) => {
      calls.push(quota)
      return `${((quota / 500000) * 6.710363).toFixed(2)} CNY`
    },
    'Credits',
    'en'
  )
  assert.equal(formatted, '4984.82 CNY')
  assert.deepEqual(calls, [371426713])
  assert.deepEqual(user, before, 'historical source counter is preserved')
})

test('old servers and unavailable historical projections show raw integer credits without fiat valuation', () => {
  for (const projection of [
    {},
    { normalized_used_quota: baseline, usage_projection_available: false },
    { normalized_used_quota: null, usage_projection_available: false },
    { normalized_used_quota: null, usage_projection_available: true },
    { normalized_used_quota: 0.5, usage_projection_available: true },
    {
      normalized_used_quota: Number.MAX_SAFE_INTEGER + 1,
      usage_projection_available: true,
    },
    { normalized_used_quota: baseline },
    { normalized_used_quota: -1, usage_projection_available: true },
  ]) {
    assert.equal(
      formatCumulativeUserUsage(
        { used_quota: raw, ...projection },
        () => {
          assert.fail('unavailable projection cannot use fiat formatter')
        },
        'Credits',
        'en'
      ),
      '2,491,782,362 Credits'
    )
  }
})

test('confirmed zero and smaller counters after a supported refund remain distinct from unavailable', () => {
  for (const normalized of [0, baseline - 500000]) {
    const user = {
      used_quota: raw,
      normalized_used_quota: normalized,
      usage_projection_available: true,
    }
    assert.equal(normalizedUserUsage(user), normalized)
    assert.equal(
      formatCumulativeUserUsage(user, String, 'Credits', 'en'),
      String(normalized)
    )
  }
  for (const used_quota of [
    undefined,
    Number.NaN,
    -1,
    1.25,
    Number.MAX_SAFE_INTEGER + 1,
  ]) {
    assert.equal(
      formatCumulativeUserUsage(
        { used_quota, usage_projection_available: false },
        String,
        'Credits',
        'en'
      ),
      '-'
    )
  }
})
