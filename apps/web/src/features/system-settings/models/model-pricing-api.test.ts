/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { api } from '@/lib/api'

import {
  acceptModelPricingResponse,
  getModelPricingConfig,
  updateModelPricingConfig,
  USD_PRICING_KEYS,
  type ModelPricingConfig,
} from './model-pricing-api'

function fixture(): ModelPricingConfig {
  return {
    schema_version: 2,
    currency: 'USD',
    storage_basis: 'legacy_pricing_unit',
    revision: 'a'.repeat(64),
    credits_per_usd: 3_600_000,
    legacy_pricing_units_per_usd: 7.2,
    model_ratio_usd_per_million: 1_000_000 / 3_600_000,
    values: Object.fromEntries(
      USD_PRICING_KEYS.map((key) => [key, '{}'])
    ) as ModelPricingConfig['values'],
  }
}

test('canonical prices are accepted once and USD writes carry revision and marker', async () => {
  const original = { get: api.get, post: api.post }
  const config = fixture()
  config.values.ModelPrice = '{"request":1.25,"free":0}'
  config.values['billing_setting.billing_expr'] =
    '{"tiered":"(tier(\\"base\\", p * 7.2)) / 7.2"}'
  const requests: unknown[] = []
  api.get = (async (url: string) => {
    assert.equal(url, '/api/option/pricing')
    return { data: { success: true, data: config } }
  }) as typeof api.get
  api.post = (async (url: string, request: unknown) => {
    requests.push({ url, request })
    return { data: { success: true, data: config } }
  }) as typeof api.post
  try {
    const loaded = await getModelPricingConfig()
    assert.equal(loaded.values.ModelPrice, config.values.ModelPrice)
    const values = {
      ModelPrice: '{"request":2,"free":0}',
      'billing_setting.billing_expr':
        config.values['billing_setting.billing_expr'],
    }
    await updateModelPricingConfig(loaded, values, true)
    await updateModelPricingConfig(loaded, values)
    for (const [index, request] of requests.entries()) {
      assert.deepEqual(request, {
        url: `/api/option/pricing/${index === 0 ? 'validate' : 'bulk'}`,
        request: {
          schema_version: 2,
          currency: 'USD',
          expected_revision: config.revision,
          values,
        },
      })
    }
  } finally {
    api.get = original.get
    api.post = original.post
  }
})

test('old providers and revision conflicts never retry a legacy USD write', async () => {
  const original = { get: api.get, post: api.post, put: api.put }
  const requests: string[] = []
  api.get = (async (url: string) => {
    requests.push(url)
    throw new Error('404')
  }) as typeof api.get
  api.post = (async (url: string) => {
    requests.push(url)
    throw new Error('409 stale revision')
  }) as typeof api.post
  api.put = (async () => {
    throw new Error('legacy write must never be called')
  }) as typeof api.put
  try {
    await assert.rejects(getModelPricingConfig(), /404/)
    await assert.rejects(
      updateModelPricingConfig(fixture(), { ModelPrice: '{"request":0}' }),
      /409/
    )
    assert.deepEqual(requests, [
      '/api/option/pricing',
      '/api/option/pricing/bulk',
    ])
  } finally {
    api.get = original.get
    api.post = original.post
    api.put = original.put
  }
})

test('legacy unit snapshots cannot enter a USD editor', () => {
  for (const invalid of [
    { ...fixture(), currency: 'CNY' },
    { ...fixture(), schema_version: 1 },
    { ...fixture(), revision: '' },
    { ...fixture(), credits_per_usd: 0 },
  ]) {
    assert.throws(() =>
      acceptModelPricingResponse({
        success: true,
        data: invalid as ModelPricingConfig,
      })
    )
  }
})
