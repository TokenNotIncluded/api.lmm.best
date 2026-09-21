/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'

import { usageLogSchema } from '@/features/usage-logs/data/schema'
import { api } from '@/lib/api'

import { latestRequestStatus, loadLatestRequest } from './latest-request'
const originalGet = api.get
const fixture = (type: number, created_at: number, other = '') =>
  usageLogSchema.parse({
    id: type,
    user_id: 1,
    type,
    created_at,
    content: '',
    other,
  })
afterEach(() => {
  api.get = originalGet
})
test('selects the latest error or usage entry instead of the latest payment', async () => {
  api.get = (async (path: string) => ({
    data: {
      success: true,
      data: {
        items: [path.includes('type=5') ? fixture(5, 200) : fixture(2, 100)],
      },
    },
  })) as typeof api.get
  assert.equal((await loadLatestRequest())?.type, 5)
})
test('does not turn a failed log query into an empty request history', async () => {
  api.get = (async () => ({ data: { success: false } })) as typeof api.get
  await assert.rejects(loadLatestRequest())
})
test('usage accounting alone is not evidence of a successful response', () => {
  assert.equal(latestRequestStatus(fixture(2, 100)), 'recorded')
  assert.equal(
    latestRequestStatus(fixture(2, 100, '{"acquisition_success_v1":true}')),
    'successful'
  )
  assert.equal(
    latestRequestStatus(
      fixture(
        2,
        100,
        '{"acquisition_success_v1":true,"stream_status":{"status":"error"}}'
      )
    ),
    'failed'
  )
  assert.equal(latestRequestStatus(fixture(5, 100)), 'failed')
})
