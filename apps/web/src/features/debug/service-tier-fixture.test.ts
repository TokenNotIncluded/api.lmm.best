/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { AxiosHeaders, type InternalAxiosRequestConfig } from 'axios'
import { Window } from 'happy-dom'

import type { ServiceTierState } from '@/features/system-settings/models/service-tier-api'

const domWindow = new Window({ url: 'http://127.0.0.1:4174/' })
Object.defineProperty(globalThis, 'window', {
  configurable: true,
  value: domWindow,
})
const { consolePageFixture } = await import('./console-page-fixtures')
const { useAuthStore } = await import('@/stores/auth-store')
const config = (url: string, method = 'get') =>
  ({ url, method, headers: new AxiosHeaders() }) as InternalAxiosRequestConfig
const path = '/api/ratio_sync/service_tiers'

test('service-tier preview is root-only, read-only and disabled without prices', () => {
  const previousUser = useAuthStore.getState().auth.user
  try {
    useAuthStore.getState().auth.setUser({ id: 9001, role: 100 })
    const first = consolePageFixture(config(path)) as {
      success: boolean
      data: ServiceTierState
    }
    assert.equal(first.success, true)
    assert.equal(first.data.policy.enabled, false)
    assert.equal(first.data.policy.fast_markup, 1.2)
    assert.equal(first.data.policy.ultrafast_markup, 1.2)
    assert.deepEqual(first.data.policy.fast_groups, [])
    assert.deepEqual(first.data.policy.ultrafast_groups, [])
    assert.equal(first.data.fresh, false)
    assert.equal(first.data.max_age_hours, 24)
    assert.deepEqual(first.data.catalog.models, {})
    first.data.policy.enabled = true
    const second = consolePageFixture(config(path)) as {
      data: ServiceTierState
    }
    assert.equal(second.data.policy.enabled, false)
    assert.equal(consolePageFixture(config(path, 'put')), undefined)
    assert.equal(consolePageFixture(config(`${path}/sync`, 'post')), undefined)
    assert.equal(
      consolePageFixture(config(`https://example.invalid${path}`)),
      undefined
    )
    assert.equal(
      consolePageFixture(config(`http://user@127.0.0.1:4174${path}`)),
      undefined
    )
    for (const role of [0, 1, 10]) {
      useAuthStore.getState().auth.setUser({ id: 9001, role })
      assert.equal(consolePageFixture(config(path)), undefined)
    }
  } finally {
    useAuthStore.getState().auth.setUser(previousUser)
  }
})
