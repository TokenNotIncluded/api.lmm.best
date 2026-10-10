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

test('null service-tier group lists are empty without changing policy or state', async () => {
  const originalGet = api.get
  try {
    for (const lists of [
      { fast_groups: null, ultrafast_groups: null },
      { fast_groups: ['default'], ultrafast_groups: null },
      { fast_groups: null, ultrafast_groups: ['vip'] },
    ]) {
      const state = {
        policy: {
          enabled: false,
          fast_markup: 1.3,
          ultrafast_markup: 1.9,
          ...lists,
        },
        catalog: {
          source: '',
          fetched_at: '0001-01-01T00:00:00Z',
          sha256: '',
          models: null,
        },
        fresh: false,
        max_age_hours: 24,
        groups: { default: 1, vip: 2 },
      }
      const before = JSON.stringify(state)
      api.get = (async () => ({
        data: { success: true, data: state },
      })) as typeof api.get
      const result = await getServiceTierPricing()
      assert.deepEqual(result, {
        ...state,
        policy: {
          ...state.policy,
          fast_groups: lists.fast_groups ?? [],
          ultrafast_groups: lists.ultrafast_groups ?? [],
        },
      })
      assert.equal(result.catalog, state.catalog)
      assert.equal(result.groups, state.groups)
      assert.equal(JSON.stringify(state), before)
    }
  } finally {
    api.get = originalGet
  }
})

test('omitted service-tier group lists are empty for valid policy responses', async () => {
  const originalGet = api.get
  const state = {
    policy: {
      enabled: false,
      fast_markup: 1.3,
      ultrafast_markup: 1.9,
    },
    catalog: {
      source: '',
      fetched_at: '0001-01-01T00:00:00Z',
      sha256: '',
      models: null,
    },
    fresh: false,
    max_age_hours: 24,
    groups: { default: 1 },
  }
  api.get = (async () => ({
    data: { success: true, data: state },
  })) as typeof api.get
  try {
    const result = await getServiceTierPricing()
    assert.deepEqual(result.policy, {
      ...state.policy,
      fast_groups: [],
      ultrafast_groups: [],
    })
    assert.equal(Object.hasOwn(state.policy, 'fast_groups'), false)
    assert.equal(Object.hasOwn(state.policy, 'ultrafast_groups'), false)
  } finally {
    api.get = originalGet
  }
})
