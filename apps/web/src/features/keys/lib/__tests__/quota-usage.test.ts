/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { apiKeySchema } from '../../types'
import { getQuotaUsage } from '../quota-usage'

test('only confirmed projected usage combines with current remaining quota', () => {
  assert.deepEqual(
    getQuotaUsage({
      used_quota: 3_359_744,
      normalized_used_quota: 500_000,
      usage_projection_available: true,
      remain_quota: 500_000,
    }),
    {
      used: 500_000,
      remaining: 500_000,
      total: 1_000_000,
      remainingPercent: 50,
      usedPercent: 50,
    }
  )
})

test('zero projected usage is available and preserves the full remaining allowance', () => {
  assert.deepEqual(
    getQuotaUsage({
      used_quota: 3_359_744,
      normalized_used_quota: 0,
      usage_projection_available: true,
      remain_quota: 500_000,
    }),
    {
      used: 0,
      remaining: 500_000,
      total: 500_000,
      remainingPercent: 100,
      usedPercent: 0,
    }
  )
})

test('missing, unconfirmed and invalid projections expose no mixed-unit total or percentage', () => {
  for (const projection of [
    {},
    { normalized_used_quota: 500_000 },
    { usage_projection_available: false, normalized_used_quota: 500_000 },
    { usage_projection_available: true },
    { usage_projection_available: true, normalized_used_quota: null },
    { usage_projection_available: true, normalized_used_quota: -1 },
    { usage_projection_available: true, normalized_used_quota: 0.5 },
    { usage_projection_available: true, normalized_used_quota: Number.NaN },
    {
      usage_projection_available: true,
      normalized_used_quota: Number.MAX_SAFE_INTEGER + 1,
    },
  ]) {
    assert.deepEqual(
      getQuotaUsage({
        used_quota: 3_359_744,
        remain_quota: 500_000,
        ...projection,
      }),
      {
        used: null,
        remaining: 500_000,
        total: null,
        remainingPercent: null,
        usedPercent: null,
      }
    )
  }
})

test('invalid remaining quota or unsafe total does not create misleading progress', () => {
  for (const remaining of [-1, 0.5, Number.NaN, Number.MAX_SAFE_INTEGER]) {
    const usage = getQuotaUsage({
      used_quota: 3_359_744,
      normalized_used_quota: 500_000,
      usage_projection_available: true,
      remain_quota: remaining,
    })
    assert.equal(usage.total, null)
    assert.equal(usage.remainingPercent, null)
    assert.equal(usage.usedPercent, null)
  }
})

test('token schema retains projection fields and still accepts legacy token responses', () => {
  const base = {
    id: 1,
    name: 'test',
    key: 'masked',
    status: 1,
    remain_quota: 500_000,
    used_quota: 3_359_744,
    unlimited_quota: false,
    expired_time: -1,
    created_time: 1,
    accessed_time: 1,
    model_limits_enabled: false,
  }
  const projected = apiKeySchema.parse({
    ...base,
    normalized_used_quota: 500_000,
    usage_projection_available: true,
  })
  assert.equal(projected.normalized_used_quota, 500_000)
  assert.equal(projected.usage_projection_available, true)
  assert.equal(projected.used_quota, base.used_quota)
  assert.equal(apiKeySchema.parse(base).usage_projection_available, undefined)
})
