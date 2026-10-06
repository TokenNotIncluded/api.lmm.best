/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { QueryClient } from '@tanstack/react-query'
import {
  AxiosError,
  AxiosHeaders,
  type InternalAxiosRequestConfig,
} from 'axios'

import { createQueryRetry } from './query-retry'
import { rateLimitWaitSeconds } from './request-rate-limit'

function failure(status: number, retryAfter?: string) {
  return new AxiosError('request rejected', undefined, undefined, undefined, {
    status,
    statusText: 'rejected',
    data: '',
    config: {} as InternalAxiosRequestConfig,
    headers: new AxiosHeaders(retryAfter ? { 'Retry-After': retryAfter } : {}),
  })
}

test('actual query execution stops at one 429 attempt even with Retry-After', async () => {
  for (const retryAfter of [
    undefined,
    '180',
    new Date(Date.now() + 120000).toUTCString(),
  ]) {
    const client = new QueryClient({
      defaultOptions: {
        queries: { retry: createQueryRetry(true), retryDelay: 0 },
      },
    })
    let attempts = 0
    await assert.rejects(
      client.fetchQuery({
        queryKey: ['limited', retryAfter],
        queryFn: async () => {
          attempts++
          throw failure(429, retryAfter)
        },
      })
    )
    assert.equal(attempts, 1)
    // A later user retry/poll still runs normally; the policy does not disable the query.
    assert.equal(
      await client.fetchQuery({
        queryKey: ['limited', retryAfter],
        queryFn: async () => {
          attempts++
          return 'recovered'
        },
      }),
      'recovered'
    )
    assert.equal(attempts, 2)
    client.clear()
  }
})

test('ordinary server failures preserve four production retries and no development retries', async () => {
  for (const [production, maximum, expected] of [
    [true, 4, 5],
    [true, 1, 2],
    [false, 4, 1],
  ] as const) {
    const client = new QueryClient({
      defaultOptions: {
        queries: {
          retry: createQueryRetry(production, maximum),
          retryDelay: 0,
        },
      },
    })
    let attempts = 0
    await assert.rejects(
      client.fetchQuery({
        queryKey: ['server-error'],
        queryFn: async () => {
          attempts++
          throw failure(503)
        },
      })
    )
    assert.equal(attempts, expected)
    client.clear()
  }
})

test('existing authentication and billing failures are not retried', async () => {
  for (const status of [401, 402, 403]) {
    const client = new QueryClient({
      defaultOptions: {
        queries: { retry: createQueryRetry(true), retryDelay: 0 },
      },
    })
    let attempts = 0
    await assert.rejects(
      client.fetchQuery({
        queryKey: [status],
        queryFn: async () => {
          attempts++
          throw failure(status)
        },
      })
    )
    assert.equal(attempts, 1)
    client.clear()
  }
})

test('one-retry query overrides cannot amplify a rate limit and mutations remain single attempt', async () => {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: createQueryRetry(true), retryDelay: 0 },
    },
  })
  let queries = 0,
    mutations = 0
  await assert.rejects(
    client.fetchQuery({
      queryKey: ['override'],
      retry: createQueryRetry(true, 1),
      queryFn: async () => {
        queries++
        throw failure(429, '60')
      },
    })
  )
  const mutation = client.getMutationCache().build(client, {
    mutationFn: async () => {
      mutations++
      throw failure(429, '60')
    },
  })
  await assert.rejects(mutation.execute(undefined))
  assert.equal(queries, 1)
  assert.equal(mutations, 1)
  client.clear()
})

test('Retry-After supports delta seconds and HTTP dates without inventing a wait', () => {
  const now = Date.parse('2026-10-06T05:00:00Z')
  assert.equal(rateLimitWaitSeconds(failure(429, '180'), now), 180)
  assert.equal(
    rateLimitWaitSeconds(failure(429, 'Tue, 06 Oct 2026 05:02:00 GMT'), now),
    120
  )
  assert.equal(
    rateLimitWaitSeconds(failure(429, 'Tue, 06 Oct 2026 04:00:00 GMT'), now),
    0
  )
  for (const header of [
    undefined,
    'broken',
    '-1',
    '1.5',
    '999999999999999999999999',
  ]) {
    assert.equal(rateLimitWaitSeconds(failure(429, header), now), null)
  }
  assert.equal(rateLimitWaitSeconds(failure(503, '180'), now), null)
})
