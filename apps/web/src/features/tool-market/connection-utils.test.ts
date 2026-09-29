/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  buildMarketClientConfig,
  collectMarketPages,
  connectionStatus,
  connectionTokenInput,
  isPersonalMarketClient,
  marketEndpoint,
} from './connection-utils'

test('connection tokens default to invocation without managing installations', () => {
  assert.deepEqual(connectionTokenInput(' my-agent ', undefined, 1000000), {
    client_id: 'my-agent',
    can_invoke: true,
    can_manage: false,
    expires_at: 1000 + 7 * 86400,
  })
  const input = connectionTokenInput(
    'reader',
    {
      can_invoke: false,
      can_manage: false,
      expires_in_days: 1,
    },
    1000000
  )
  assert.equal(input.can_invoke, false)
  assert.equal(input.expires_at, 87400)
})

test('client names match reserved IDs and the UTF-8 byte boundary', () => {
  for (const client of [
    '',
    ' web-market',
    'web-market',
    'oauth:lmm-pi',
    'a\nb',
    'a'.repeat(129),
    '工'.repeat(43),
  ]) {
    assert.equal(isPersonalMarketClient(client), false, client)
  }
  assert.equal(isPersonalMarketClient('a'.repeat(128)), true)
  assert.equal(isPersonalMarketClient('工'.repeat(42)), true)
  for (const days of [0, 91, 1.5, NaN, Infinity]) {
    assert.throws(() =>
      connectionTokenInput('agent', {
        can_invoke: true,
        can_manage: false,
        expires_in_days: days,
      })
    )
  }
})

test('config keeps the secret in headers and escapes unusual client names', () => {
  const client = 'agent"\\name'
  const endpoint = marketEndpoint('https://api.example.test', '/mcp/market')
  const result = JSON.parse(
    buildMarketClientConfig(endpoint, client, 'secret-test')
  )
  assert.equal(result.mcpServers[client].url, endpoint)
  assert.deepEqual(result.mcpServers[client].headers, {
    Authorization: 'Bearer secret-test',
  })
  assert.equal(result.mcpServers[client].type, 'http')
  assert.ok(
    buildMarketClientConfig(endpoint, client).includes('YOUR_CONNECTION_TOKEN')
  )
})

test('config refuses credential-bearing and cross-origin endpoints', () => {
  for (const path of [
    'https://elsewhere.test/mcp',
    '//elsewhere.test/mcp',
    '/mcp?token=x',
    '/mcp#secret',
    '/\\elsewhere.test/mcp',
  ]) {
    assert.throws(() => marketEndpoint('https://api.example.test', path), path)
  }
  for (const endpoint of [
    'https://user:pass@api.example.test/mcp',
    'https://api.example.test/mcp?token=x',
    'javascript:alert(1)',
  ]) {
    assert.throws(() => buildMarketClientConfig(endpoint, 'agent'), endpoint)
  }
  assert.equal(
    marketEndpoint('http://localhost:3000', '/mcp/market'),
    'http://localhost:3000/mcp/market'
  )
})

test('expiry is not shown as an active connection and revocation wins', () => {
  assert.equal(
    connectionStatus({ revoked_at: 0, expires_at: 1000 }, 1000000),
    'expired'
  )
  assert.equal(
    connectionStatus({ revoked_at: 0, expires_at: 1001 }, 1000000),
    'active'
  )
  assert.equal(
    connectionStatus({ revoked_at: 1, expires_at: 1001 }, 1000000),
    'revoked'
  )
})

test('account resources traverse all pages including the exact page boundary', async () => {
  const offsets: number[] = []
  const result = await collectMarketPages(async (offset, limit) => {
    offsets.push(offset)
    return Array.from(
      { length: Math.min(limit, 200 - offset) },
      (_, index) => offset + index
    )
  })
  assert.deepEqual(offsets, [0, 100, 200])
  assert.equal(result.length, 200)
  assert.equal(result[199], 199)
})

test('pagination errors never resolve with a partial permission snapshot', async () => {
  await assert.rejects(
    collectMarketPages(async (offset) => {
      if (offset) throw new Error('network failed')
      return Array.from({ length: 100 }, (_, index) => index)
    }),
    /network failed/
  )
  await assert.rejects(
    collectMarketPages(async () => [1, 2], 2, 2),
    /pagination limit/
  )
  await assert.rejects(
    collectMarketPages(async () => [1, 2, 3], 2),
    /Invalid account/
  )
  await assert.rejects(
    collectMarketPages(async () => [], 0),
    /Invalid pagination/
  )
})
