/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { marketOAuthCommand } from './oauth-command'

test('market OAuth offers browser authorization without a pasted key', () => {
  const command = marketOAuthCommand('https://api.lmm.best/mcp/market')
  assert.ok(
    command.includes('codex mcp add lmm --url https://api.lmm.best/mcp/market')
  )
  assert.ok(command.includes('codex mcp login lmm'))
  assert.ok(!command.includes('Bearer'))
  assert.ok(!command.includes('TOKEN'))
})

test('market OAuth commands reject credentials, query fields, fragments and shell syntax', () => {
  for (const value of [
    'https://u:p@api.lmm.best/mcp',
    'https://api.lmm.best/mcp?x=1',
    'https://api.lmm.best/mcp#x',
    'https://api.lmm.best/mcp;echo',
    'https://api.lmm.best/$(id)',
    'file:///mcp',
  ]) {
    assert.throws(() => marketOAuthCommand(value))
  }
})

test('compact OAuth connection quotes the URL without changing the resource path', () => {
  const command = marketOAuthCommand(
    'https://api.lmm.best/mcp/market?mode=compact'
  )
  assert.ok(
    command.includes("--url 'https://api.lmm.best/mcp/market?mode=compact'")
  )
  assert.ok(command.includes('codex mcp login lmm'))
  assert.ok(!command.includes('TOKEN'))
  for (const endpoint of [
    'https://api.lmm.best/mcp/market?mode=compact&mode=full',
    'https://api.lmm.best/mcp/market?mode=compact&token=secret',
    'https://api.lmm.best/mcp/market?mode=unknown',
    'https://api.lmm.best/mcp/market?mode=compact;echo',
    'https://api.lmm.best/mcp/market?mode=compact#fragment',
    'https://api.lmm.best/mcp/market?mode=compact\nwhoami',
  ]) {
    assert.throws(() => marketOAuthCommand(endpoint))
  }
})
