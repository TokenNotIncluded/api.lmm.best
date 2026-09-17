/*
Copyright (C) 2026 LIghtJUNction
*/
import type { SystemOptionsResponse, UpdateOptionResponse } from '../types'

export const MODEL_PRICE_SNAPSHOT_KEYS = [
  'ModelPriceLock',
  'ModelPrice',
  'ModelRatio',
  'CompletionRatio',
  'CacheRatio',
  'CreateCacheRatio',
  'ImageRatio',
  'AudioRatio',
  'AudioCompletionRatio',
  'billing_setting.billing_mode',
  'billing_setting.billing_expr',
] as const

const pricingKeys = new Set<string>(MODEL_PRICE_SNAPSHOT_KEYS)

type PricingSnapshot = Record<string, string>

function object(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function validateSnapshot(value: unknown): PricingSnapshot {
  if (
    !object(value) ||
    Object.keys(value).length !== pricingKeys.size ||
    Object.keys(value).some((key) => !pricingKeys.has(key))
  ) {
    throw new Error('Failed to load settings')
  }
  for (const key of MODEL_PRICE_SNAPSHOT_KEYS) {
    const encoded = value[key]
    if (typeof encoded !== 'string') {
      throw new Error('Failed to load settings')
    }
    let entries: unknown
    try {
      entries = JSON.parse(encoded)
    } catch {
      throw new Error('Failed to load settings')
    }
    if (
      !object(entries) ||
      Object.entries(entries).some(([name, entry]) => {
        if (!name.trim()) return true
        if (key === 'ModelPriceLock') return typeof entry !== 'boolean'
        if (key.startsWith('billing_setting.')) {
          return typeof entry !== 'string'
        }
        return typeof entry !== 'number' || !Number.isFinite(entry)
      })
    ) {
      throw new Error('Failed to load settings')
    }
  }
  return Object.fromEntries(
    MODEL_PRICE_SNAPSHOT_KEYS.map((key) => [key, value[key] as string])
  )
}

// An explicit malformed receipt is an error, not permission to substitute an
// unrelated GET. Older providers without the field retain the legacy path.
export async function resolvePriceLockSnapshot(
  response: UpdateOptionResponse & { pricing?: unknown },
  name: string,
  locked: boolean,
  load: () => Promise<SystemOptionsResponse>
): Promise<{
  pricing: PricingSnapshot
  legacyOptions?: SystemOptionsResponse
}> {
  if (!response.success) {
    throw new Error(response.message || 'Failed to update price lock')
  }
  let raw: unknown
  let legacyOptions: SystemOptionsResponse | undefined
  if (Object.hasOwn(response, 'pricing')) {
    raw = response.pricing
  } else {
    legacyOptions = await load()
    if (
      !legacyOptions.success ||
      legacyOptions.capabilities?.model_price_locks !== true
    ) {
      throw new Error(legacyOptions.message || 'Failed to load settings')
    }
    const entries = legacyOptions.data.filter(({ key }) => pricingKeys.has(key))
    if (new Set(entries.map(({ key }) => key)).size !== entries.length) {
      throw new Error('Failed to load settings')
    }
    raw = Object.fromEntries(entries.map(({ key, value }) => [key, value]))
  }
  const pricing = validateSnapshot(raw)
  const locks = JSON.parse(pricing.ModelPriceLock) as Record<string, boolean>
  const actual = Object.hasOwn(locks, name) && locks[name] === true
  if (actual !== locked) {
    throw new Error('Failed to update price lock')
  }
  return { pricing, legacyOptions }
}

// Merge only the receipt's pricing fields. An in-flight lock must not restore
// stale unrelated settings over a newer query-cache value.
export function mergePriceLockSnapshot(
  base: SystemOptionsResponse,
  pricing: PricingSnapshot
): SystemOptionsResponse {
  return {
    ...base,
    data: [
      ...base.data.filter(({ key }) => !pricingKeys.has(key)),
      ...MODEL_PRICE_SNAPSHOT_KEYS.map((key) => ({ key, value: pricing[key] })),
    ],
  }
}
