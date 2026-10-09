/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { api } from '@/lib/api'
import { getRefundPolicy } from './api'

test('refund policy uses the language-aware public endpoint', async () => {
  const original = api.get
  const calls: unknown[][] = []
  api.get = (async (...args: unknown[]) => {
    calls.push(args)
    return { data: { success: true, data: 'published refund text' } }
  }) as typeof api.get
  try {
    const result = await getRefundPolicy('en')
    assert.equal(result.data, 'published refund text')
    assert.equal(calls[0][0], '/api/refund-policy')
    assert.deepEqual(calls[0][1], { params: { lang: 'en' } })
  } finally { api.get = original }
})
