/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { SystemOptionsResponse } from '../types'
import {
  MODEL_PRICE_SNAPSHOT_KEYS,
  mergePriceLockSnapshot,
  resolvePriceLockSnapshot,
} from './model-price-lock-snapshot'

function snapshot(locked = true): Record<string, string> {
  return {
    ...Object.fromEntries(MODEL_PRICE_SNAPSHOT_KEYS.map((key) => [key, '{}'])),
    ModelPriceLock: JSON.stringify({ source: locked, other: true }),
    ModelRatio: '{"source":2,"other":3}',
  }
}

function options(pricing = snapshot()): SystemOptionsResponse {
  return {
    success: true,
    message: '',
    capabilities: { model_price_locks: true },
    data: Object.entries(pricing).map(([key, value]) => ({ key, value })),
  }
}

const success = { success: true, message: '' }
const neverLoad = async (): Promise<SystemOptionsResponse> => {
  throw new Error('receipt path must not issue another GET')
}

test('committed snapshot succeeds without a follow-up GET', async () => {
  const pricing = snapshot()
  const result = await resolvePriceLockSnapshot(
    { ...success, pricing },
    'source',
    true,
    neverLoad
  )
  assert.deepEqual(result.pricing, pricing)
  assert.equal(result.legacyOptions, undefined)
  assert.notEqual(result.pricing, pricing)
})

test('snapshot replaces pricing and preserves unrelated cache', async () => {
  const old = options(snapshot(false))
  old.data.push({ key: 'Notice', value: 'latest unrelated edit' })
  const receipt = await resolvePriceLockSnapshot(
    { ...success, pricing: snapshot() },
    'source',
    true,
    neverLoad
  )
  const merged = mergePriceLockSnapshot(old, receipt.pricing)
  assert.equal(
    merged.data.find(({ key }) => key === 'Notice')?.value,
    'latest unrelated edit'
  )
  assert.equal(
    merged.data.find(({ key }) => key === 'ModelPriceLock')?.value,
    snapshot().ModelPriceLock
  )
  assert.equal(
    old.data.find(({ key }) => key === 'ModelPriceLock')?.value,
    snapshot(false).ModelPriceLock
  )
  assert.equal(
    new Set(merged.data.map(({ key }) => key)).size,
    merged.data.length
  )
})

test('legacy provider performs one checked post-write read', async () => {
  let reads = 0
  const server = options()
  const result = await resolvePriceLockSnapshot(
    success,
    'source',
    true,
    async () => {
      reads++
      return server
    }
  )
  assert.equal(reads, 1)
  assert.equal(result.legacyOptions, server)
  assert.deepEqual(result.pricing, snapshot())
})

test('legacy read failures and missing capability do not pass', async () => {
  for (const load of [
    neverLoad,
    async () => ({ ...options(), success: false }),
    async () => ({ ...options(), capabilities: undefined }),
  ]) {
    await assert.rejects(
      resolvePriceLockSnapshot(success, 'source', true, load)
    )
  }
  await assert.rejects(
    resolvePriceLockSnapshot(
      { success: false, message: 'rejected' },
      'source',
      true,
      neverLoad
    ),
    /rejected/
  )
})

test('malformed receipts cannot fall back to stale GET', async () => {
  const missing = snapshot()
  delete missing.ModelRatio
  for (const pricing of [
    null,
    undefined,
    [],
    'snapshot',
    {},
    missing,
    { ...snapshot(), api_key: 'not allowed' },
  ]) {
    await assert.rejects(
      resolvePriceLockSnapshot(
        { ...success, pricing },
        'source',
        true,
        neverLoad
      ),
      /Failed to load settings/
    )
  }
})

test('invalid pricing types, JSON and lock booleans are rejected', async () => {
  for (const [key, value] of [
    ['ModelPrice', 'null'],
    ['ModelPrice', '[]'],
    ['ModelPrice', '{'],
    ['ModelPrice', '{"source":"2"}'],
    ['ModelPrice', '{"source":1e400}'],
    ['ModelPrice', '{" ":2}'],
    ['ModelPriceLock', '{"source":"true"}'],
    ['billing_setting.billing_expr', '{"source":2}'],
  ]) {
    await assert.rejects(
      resolvePriceLockSnapshot(
        { ...success, pricing: { ...snapshot(), [key]: value } },
        'source',
        true,
        neverLoad
      )
    )
  }
})

test('receipt must match lock and unlock intent', async () => {
  for (const locked of [true, false]) {
    await resolvePriceLockSnapshot(
      { ...success, pricing: snapshot(locked) },
      'source',
      locked,
      neverLoad
    )
    await assert.rejects(
      resolvePriceLockSnapshot(
        { ...success, pricing: snapshot(!locked) },
        'source',
        locked,
        neverLoad
      ),
      /Failed to update price lock/
    )
  }
})

test('legacy duplicated pricing keys are ambiguous', async () => {
  const server = options()
  server.data.push({ key: 'ModelPriceLock', value: '{}' })
  await assert.rejects(
    resolvePriceLockSnapshot(success, 'source', true, async () => server),
    /Failed to load settings/
  )
})

test('stale legacy state is not reported as success', async () => {
  await assert.rejects(
    resolvePriceLockSnapshot(success, 'source', true, async () =>
      options(snapshot(false))
    ),
    /Failed to update price lock/
  )
})

test('prototype names require an own boolean entry', async () => {
  const pricing = snapshot()
  pricing.ModelPriceLock = '{"__proto__":true,"constructor":true}'
  for (const name of ['__proto__', 'constructor']) {
    await resolvePriceLockSnapshot(
      { ...success, pricing },
      name,
      true,
      neverLoad
    )
    await assert.rejects(
      resolvePriceLockSnapshot(
        { ...success, pricing: snapshot() },
        name,
        true,
        neverLoad
      )
    )
  }
})
