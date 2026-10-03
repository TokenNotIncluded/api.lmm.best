/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { ToolInput } from './api'
import {
  editorCredentialWrite,
  refreshToolDefinitions,
  type EditableTools,
} from './service-editor-utils'

const tool = (name: string, patch: Partial<ToolInput> = {}): ToolInput => ({
  name,
  description: name,
  input_schema: { type: 'object', properties: { query: { type: 'string' } } },
  permissions: ['read'],
  price_quota: 0,
  ...patch,
})

test('refresh keeps owner prices, selection and permissions, and excludes new tools', () => {
  const previous: EditableTools = {
    tools: [
      tool('search', { permissions: ['read', 'network'], price_quota: 25000 }),
      tool('send', { permissions: ['send'], price_quota: 50000 }),
      tool('removed'),
    ],
    selected: ['search', 'removed'],
    prices: { search: '0.05', send: '0.1', removed: '0' },
  }
  const untouched = structuredClone(previous)
  const refreshed = refreshToolDefinitions(
    previous,
    [tool('search'), tool('send'), tool('new_tool')],
    { firstDiscovery: false, endpointChanged: false }
  )
  assert.deepEqual(refreshed.selected, ['search'])
  assert.deepEqual(refreshed.prices, {
    search: '0.05',
    send: '0.1',
    new_tool: '0',
  })
  assert.deepEqual(refreshed.tools[0].permissions, ['read', 'network'])
  assert.equal(refreshed.tools[0].price_quota, 25000)
  assert.deepEqual(refreshed.changes.added, ['new_tool'])
  assert.deepEqual(refreshed.changes.removed, ['removed'])
  assert.deepEqual(previous, untouched)
})

test('schema and endpoint changes are reviewable without resetting edited policy', () => {
  const previous: EditableTools = {
    tools: [tool('search', { permissions: ['read', 'files'] })],
    selected: ['search'],
    prices: { search: '12.5' },
  }
  const refreshed = refreshToolDefinitions(
    previous,
    [tool('search', { input_schema: { type: 'object', required: ['query'] } })],
    { firstDiscovery: false, endpointChanged: true }
  )
  assert.deepEqual(refreshed.changes.changed, ['search'])
  assert.equal(refreshed.changes.endpointChanged, true)
  assert.deepEqual(refreshed.selected, ['search'])
  assert.deepEqual(refreshed.tools[0].permissions, ['read', 'files'])
  assert.equal(refreshed.prices.search, '12.5')
})

test('key ordering does not flag a schema change and only first discovery selects tools', () => {
  const previous: EditableTools = {
    tools: [tool('search')],
    selected: [],
    prices: { search: '0.05' },
  }
  const refreshed = refreshToolDefinitions(
    previous,
    [
      tool('search', {
        input_schema: {
          properties: { query: { type: 'string' } },
          type: 'object',
        },
      }),
    ],
    { firstDiscovery: false, endpointChanged: false }
  )
  assert.deepEqual(refreshed.changes.changed, [])
  assert.deepEqual(refreshed.selected, [])
  const first = refreshToolDefinitions(
    { tools: [], selected: [], prices: {} },
    [tool('search')],
    { firstDiscovery: true, endpointChanged: false }
  )
  assert.deepEqual(first.selected, ['search'])
})

test('stored credentials can only be copied to the same endpoint and authentication mode', () => {
  const choice = {
    mode: 'bearer' as const,
    secret: '',
    stored: { mode: 'bearer' as const, configured: true },
    storedVersionID: 'old-version',
    sameEndpoint: true,
  }
  assert.deepEqual(editorCredentialWrite(choice), {
    mode: 'bearer',
    copy_from_version_id: 'old-version',
  })
  assert.throws(() => editorCredentialWrite({ ...choice, sameEndpoint: false }))
  assert.throws(() => editorCredentialWrite({ ...choice, mode: 'api_key' }))
  assert.deepEqual(editorCredentialWrite({ ...choice, mode: 'none' }), {
    mode: 'none',
  })
  assert.deepEqual(
    editorCredentialWrite({
      ...choice,
      secret: 'fixture-only',
      sameEndpoint: false,
    }),
    {
      mode: 'bearer',
      secret: 'fixture-only',
    }
  )
})

test('credential validation uses UTF-8 byte limits and rejects unsafe header characters', () => {
  const choice = {
    mode: 'bearer' as const,
    stored: undefined,
    storedVersionID: undefined,
    sameEndpoint: false,
  }
  for (const secret of [
    'has space',
    'has\u00a0space',
    'line\nbreak',
    'control\u0000byte',
    '界'.repeat(1366),
  ]) {
    assert.throws(
      () => editorCredentialWrite({ ...choice, secret }),
      /Invalid credential/
    )
  }
  assert.equal(
    editorCredentialWrite({
      ...choice,
      mode: 'api_key',
      secret: 'allowed space',
    }).secret,
    'allowed space'
  )
  assert.equal(
    editorCredentialWrite({ ...choice, secret: 'a'.repeat(4096) }).secret
      ?.length,
    4096
  )
})

test('refresh retains exact schema text and identifies adjacent integers beyond JavaScript precision', () => {
  const before = '{"type":"object","minimum":9007199254740992}'
  const after = '{"type":"object","minimum":9007199254740993}'
  const output = '{"type":"object","maximum":9007199254740995}'
  const previous: EditableTools = {
    tools: [
      tool('search', {
        input_schema: before,
        permissions: ['read', 'network'],
        price_quota: 25000,
      }),
    ],
    selected: ['search'],
    prices: { search: '0.05' },
  }
  const refreshed = refreshToolDefinitions(
    previous,
    [
      tool('search', {
        input_schema: JSON.parse(after),
        input_schema_json: after,
        output_schema: JSON.parse(output),
        output_schema_json: output,
      }),
    ],
    { firstDiscovery: false, endpointChanged: false }
  )
  assert.equal(refreshed.tools[0].input_schema, after)
  assert.equal(refreshed.tools[0].output_schema, output)
  assert.equal('input_schema_json' in refreshed.tools[0], false)
  assert.deepEqual(refreshed.changes.changed, ['search'])
  assert.deepEqual(refreshed.selected, ['search'])
  assert.equal(refreshed.prices.search, '0.05')
  assert.deepEqual(refreshed.tools[0].permissions, ['read', 'network'])
})

test('refresh refuses a legacy parsed schema whose integer precision is unavailable', () => {
  const previous: EditableTools = {
    tools: [tool('search')],
    selected: ['search'],
    prices: { search: '0.05' },
  }
  const untouched = structuredClone(previous)
  assert.throws(
    () =>
      refreshToolDefinitions(
        previous,
        [
          tool('search', {
            input_schema: {
              type: 'object',
              properties: {
                id: { type: 'integer', default: Number('9007199254740993') },
              },
            },
          }),
        ],
        { firstDiscovery: false, endpointChanged: false }
      ),
    /original JSON/
  )
  assert.deepEqual(previous, untouched)
})
