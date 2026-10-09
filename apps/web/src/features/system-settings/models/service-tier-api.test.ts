/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { api } from '@/lib/api'

import {
  getServiceTierPricing,
  saveServiceTierPricing,
  syncServiceTierPricing,
} from './service-tier-api'

test('tier sync is one catalog request and never writes model prices or enables the policy', async () => {
  const original = { get: api.get, post: api.post, put: api.put }
  const policy = {
    enabled: false,
    fast_markup: 1.2,
    ultrafast_markup: 1.2,
    fast_groups: [],
    ultrafast_groups: [],
  }
  const state = { policy, catalog: { models: {} }, fresh: false, groups: {} }
  const requests: unknown[] = []
  api.get = (async (url: string) => {
    requests.push(['get', url])
    return { data: { success: true, data: state } }
  }) as typeof api.get
  api.post = (async (url: string, body?: unknown) => {
    requests.push(['post', url, body])
    return { data: { success: true, data: state } }
  }) as typeof api.post
  api.put = (async (url: string, body: unknown) => {
    requests.push(['put', url, body])
    return { data: { success: true, data: state } }
  }) as typeof api.put
  try {
    assert.equal((await getServiceTierPricing()).policy.enabled, false)
    await syncServiceTierPricing()
    await saveServiceTierPricing(policy)
    assert.deepEqual(requests, [
      ['get', '/api/ratio_sync/service_tiers'],
      ['post', '/api/ratio_sync/service_tiers/sync', undefined],
      ['put', '/api/ratio_sync/service_tiers', policy],
    ])
    api.post = (async () => ({
      data: { success: false, message: 'old catalog retained' },
    })) as typeof api.post
    await assert.rejects(syncServiceTierPricing(), /old catalog retained/)
  } finally {
    Object.assign(api, original)
  }
})
