/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { api } from '@/lib/api'

import { metaDelegationAPI, metaDelegationQuota } from './meta-delegation-api'

test('meta delegation keeps exact nonnegative integer credit inputs', () => {
  assert.equal(metaDelegationQuota('0'), 0)
  assert.equal(metaDelegationQuota('500000'), 500000)
  assert.equal(metaDelegationQuota('9007199254740991'), Number.MAX_SAFE_INTEGER)
  for (const value of [
    '-1',
    '1.0',
    '0.1',
    '9e1',
    ' 1',
    '1 ',
    '03',
    '9007199254740992',
    '9007199254740991.1',
    '',
    'Infinity',
  ]) {
    assert.equal(metaDelegationQuota(value), undefined, value)
  }
})

test('meta delegation request has no dollar conversion, identity aliases or implicit unlimited value', async () => {
  const original = api.put
  const calls: { url: string; body: unknown }[] = []
  api.put = (async (url, body) => {
    calls.push({ url, body })
    return {
      data: {
        success: true,
        data: {
          enabled: true,
          max_total_quota: 500000,
          expires_at: 2000000000,
          updated_at: 1,
        },
      },
    }
  }) as typeof api.put
  try {
    const input = {
      enabled: true,
      max_total_quota: 500000,
      expires_at: 2000000000,
    }
    const result = await metaDelegationAPI.set(
      { kind: 'personal', id: 'token-id' },
      input
    )
    assert.equal(result.max_total_quota, 500000)
    assert.deepEqual(calls, [
      {
        url: '/api/tool-market/meta-delegations/personal/token-id',
        body: input,
      },
    ])
    for (const quota of [-1, 0.5, Number.MAX_SAFE_INTEGER + 1, Infinity]) {
      assert.throws(() =>
        metaDelegationAPI.set(
          { kind: 'personal', id: 'token-id' },
          { enabled: true, max_total_quota: quota, expires_at: 0 }
        )
      )
    }
    assert.equal(calls.length, 1, 'invalid credit inputs never reach the API')
  } finally {
    api.put = original
  }
})

test('meta delegation owner API encodes OAuth client target and masks private error text', async () => {
  const original = api.get
  const urls: string[] = []
  api.get = (async (url) => {
    urls.push(url)
    throw {
      response: {
        data: { code: 'TOOL_MARKET_DENIED', message: 'private-SQL-and-token' },
      },
    }
  }) as typeof api.get
  try {
    await assert.rejects(
      metaDelegationAPI.get({ kind: 'oauth', id: 'oauth:agent' }),
      (error: Error) => error.message === 'TOOL_MARKET_DENIED'
    )
    assert.deepEqual(urls, [
      '/api/tool-market/meta-delegations/oauth/oauth%3Aagent',
    ])
  } finally {
    api.get = original
  }
})
