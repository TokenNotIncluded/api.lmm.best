/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  formatHistoricalRawQuota,
  historicalLogQuotaUSD,
  historicalQuotaUSD,
} from './historical-quota-money'

test('historical money requires a complete typed projection with a known basis', () => {
  const valid = {
    quota_money_available: true,
    quota_money_basis: 'captured_credits_per_usd',
    quota_usd: '0.000002',
  }
  assert.equal(historicalQuotaUSD(valid), 0.000002)
  for (const invalid of [
    {},
    { ...valid, quota_money_available: false },
    { ...valid, quota_money_basis: 'aggregate_missing_credit_basis' },
    { ...valid, quota_money_basis: 'projection_limit' },
    { ...valid, unpriced_quota_records: 1 },
    { ...valid, quota_projection_truncated: true },
    { ...valid, quota_usd: 'NaN' },
    { ...valid, quota_usd: '-1' },
    { ...valid, quota_usd: '1e5' },
    { ...valid, quota_usd: '9'.repeat(400) },
    { ...valid, quota_money_basis: 'empty_result' },
  ]) {
    assert.equal(historicalQuotaUSD(invalid), null)
  }
  assert.equal(
    historicalQuotaUSD({
      ...valid,
      quota_money_basis: 'empty_result',
      quota_usd: '0',
    }),
    0
  )
})

test('legacy costs use only a numeric captured anchor and preserve explicit unavailable', () => {
  const entry = {
    quota: 3_500_000,
    other: { pricing_credits_per_usd: 3_500_000 },
  }
  assert.equal(historicalLogQuotaUSD(entry), 1)
  assert.equal(historicalLogQuotaUSD({ ...entry, other: null }), null)
  for (const anchor of [
    '3500000',
    0,
    -1,
    1.5,
    Infinity,
    Number.MAX_SAFE_INTEGER + 1,
  ]) {
    assert.equal(
      historicalLogQuotaUSD({
        ...entry,
        other: { pricing_credits_per_usd: anchor },
      }),
      null
    )
  }
  assert.equal(
    historicalLogQuotaUSD({
      ...entry,
      projection: { quota_money_available: false },
    }),
    null
  )
  for (const projection of [
    { quota_projection_truncated: true },
    { unpriced_quota_records: 1 },
    {
      quota_money_available: true,
      quota_money_basis: 'empty_result',
      quota_usd: '0',
    },
  ]) {
    assert.equal(historicalLogQuotaUSD({ ...entry, type: 2, projection }), null)
  }
  assert.equal(historicalLogQuotaUSD({ ...entry, type: 6 }), null)
  assert.equal(historicalLogQuotaUSD({ ...entry, quota: 0.5 }), null)
})

test('subscription subamounts need their own captured anchor and unknown amounts remain raw', () => {
  const entry = {
    quota: 5000,
    consumedQuota: 2500,
    other: { pricing_credits_per_usd: 500000 },
    projection: {
      quota_money_available: true,
      quota_money_basis: 'captured_credits_per_usd',
      quota_usd: '0.01',
    },
  }
  assert.equal(historicalLogQuotaUSD(entry), 0.005)
  assert.equal(historicalLogQuotaUSD({ ...entry, other: null }), null)
  assert.equal(
    formatHistoricalRawQuota(3500000, 'Raw credits', 'en'),
    '3,500,000 Raw credits'
  )
  assert.equal(
    formatHistoricalRawQuota(-1, 'Raw credits', 'en'),
    '-1 Raw credits'
  )
  assert.equal(
    formatHistoricalRawQuota('9007199254740993', 'Raw credits', 'en'),
    '9,007,199,254,740,993 Raw credits'
  )
  assert.equal(formatHistoricalRawQuota(0.5, 'Raw credits'), '—')
})
