import assert from 'node:assert/strict'
import { test } from 'node:test'
import { marketOAuthCommand } from './oauth-connection'

test('market OAuth offers browser authorization without a pasted key', () => {
  const command = marketOAuthCommand('https://api.lmm.best/mcp/market')
  assert.ok(command.includes('codex mcp add lmm --url https://api.lmm.best/mcp/market'))
  assert.ok(command.includes('codex mcp login lmm'))
  assert.ok(!command.includes('Bearer'))
  assert.ok(!command.includes('TOKEN'))
})

test('market OAuth commands reject credentials, query fields, fragments and shell syntax', () => {
  for (const value of ['https://u:p@api.lmm.best/mcp', 'https://api.lmm.best/mcp?x=1', 'https://api.lmm.best/mcp#x', 'https://api.lmm.best/mcp;echo', 'https://api.lmm.best/$(id)', 'file:///mcp']) {
    assert.throws(() => marketOAuthCommand(value))
  }
})
