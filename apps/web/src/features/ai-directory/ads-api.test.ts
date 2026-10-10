/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { api } from '@/lib/api'

import { isUsdDirectoryAdQuote, quoteDirectoryAd } from './ads-api'

const quote = {
  pricing_schema_version: 2,
  currency: 'USD',
  bid_cents: 100,
  quota: 3_500_000,
  duration_days: 30,
  min_bid_cents: 100,
  max_bid_cents: 1_000_000,
}

test('accepts the server integer charge without rebuilding it from legacy quota units', async () => {
  const original = api.get
  api.get = (async (url: string) => {
    assert.equal(url, '/api/ai-directory/ads/quote?bid_cents=100')
    return { data: { success: true, data: quote } }
  }) as typeof api.get
  try {
    assert.deepEqual(await quoteDirectoryAd(100), quote)
  } finally {
    api.get = original
  }
})

test('old backend quotes, non-USD markers and invalid integer charges cannot authorize payment', async () => {
  const original = api.get
  for (const invalid of [
    { ...quote, pricing_schema_version: undefined },
    { ...quote, pricing_schema_version: 0 },
    { ...quote, currency: 'CNY' },
    { ...quote, quota: 0 },
    { ...quote, quota: 3.5 },
    { ...quote, quota: Number.MAX_SAFE_INTEGER + 1 },
  ]) {
    assert.equal(isUsdDirectoryAdQuote(invalid), false)
    api.get = (async () => ({
      data: { success: true, data: invalid },
    })) as typeof api.get
    try {
      await assert.rejects(quoteDirectoryAd(100), /Unable to get a price quote/)
    } finally {
      api.get = original
    }
  }
})
