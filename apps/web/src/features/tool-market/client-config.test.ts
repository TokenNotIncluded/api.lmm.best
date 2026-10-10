/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { test } from 'node:test'

import { marketClientProfiles, type MarketClientProfile } from './client-config'
import { buildMarketClientConfig } from './connection-utils'

const endpoint = 'https://api.example.test/mcp/market'
const token = 'mkt_test_secret'

test('Codex TOML parses with literal client keys and static bearer authentication', () => {
  const runtime = globalThis as typeof globalThis & {
    Bun: { TOML: { parse: (text: string) => unknown } }
  }
  for (const client of ['my-agent', 'agent.name', '工具["x"]\\path', '😀']) {
    const config = runtime.Bun.TOML.parse(
      buildMarketClientConfig(endpoint, client, token, 'codex')
    ) as {
      mcp_servers: Record<
        string,
        { url: string; http_headers: { Authorization: string } }
      >
    }
    assert.deepEqual(Object.keys(config.mcp_servers), [client])
    assert.deepEqual(config.mcp_servers[client], {
      url: endpoint,
      http_headers: { Authorization: `Bearer ${token}` },
    })
  }
})

test('Claude Code user installation survives shell metacharacters without executing them', () => {
  const client = 'agent "\'`$(exit 91);[]'
  const url = "https://api.example.test/mcp'$(exit%2091);[]"
  const command = buildMarketClientConfig(url, client, token, 'claude-code')
  // A shell stub records arguments. No real client or network is invoked.
  const result = spawnSync(
    'sh',
    ['-c', `claude() { printf '%s\\0' "$@"; }; ${command}`],
    { encoding: 'utf8' }
  )
  assert.equal(result.status, 0, result.stderr)
  const args = result.stdout.split('\0').slice(0, -1)
  assert.deepEqual(args.slice(0, 4), ['mcp', 'add-json', '--scope', 'user'])
  assert.match(args[4], /^[A-Za-z0-9_-]+$/)
  assert.deepEqual(JSON.parse(args[5]), {
    type: 'http',
    url,
    headers: { Authorization: `Bearer ${token}` },
  })
  assert.equal(
    command,
    buildMarketClientConfig(url, client, token, 'claude-code')
  )
})

test('Cursor remote config omits the Claude transport key while generic HTTP includes it', () => {
  for (const profile of ['cursor', 'http'] as const) {
    const config = JSON.parse(
      buildMarketClientConfig(endpoint, 'my-agent', token, profile)
    )
    const server = config.mcpServers['my-agent']
    assert.equal(server.url, endpoint)
    assert.deepEqual(server.headers, { Authorization: `Bearer ${token}` })
    assert.equal(server.type, profile === 'http' ? 'http' : undefined)
    assert.equal(config.url, undefined)
    assert.equal(server.url.includes(token), false)
  }
})

test('every profile preview keeps a placeholder and invalid header tokens are rejected', () => {
  for (const profile of marketClientProfiles) {
    const preview = buildMarketClientConfig(
      endpoint,
      'my-agent',
      undefined,
      profile
    )
    assert.ok(preview.includes('YOUR_CONNECTION_TOKEN'), profile)
    assert.equal(preview.includes(token), false, profile)
    for (const secret of [
      '',
      'secret\nX-Header: value',
      'secret\r',
      '${API_KEY}',
      ' secret ',
    ]) {
      assert.throws(() =>
        buildMarketClientConfig(endpoint, 'my-agent', secret, profile)
      )
    }
  }
  assert.throws(() =>
    buildMarketClientConfig(
      endpoint,
      'my-agent',
      token,
      'invalid' as MarketClientProfile
    )
  )
  assert.throws(() =>
    buildMarketClientConfig(endpoint, '\ud800', token, 'codex')
  )
})
