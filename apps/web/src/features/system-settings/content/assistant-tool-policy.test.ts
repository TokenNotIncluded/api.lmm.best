/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  DEFAULT_ASSISTANT_TOOL_POLICY,
  assistantPolicyMatchesCatalog,
  assistantToolPolicySchema,
  isAssistantToolEnabled,
  parseAssistantToolCatalog,
  parseAssistantToolPolicy,
  updateAssistantToolPolicy,
} from './assistant-tool-policy'

const catalogPayload = {
  success: true,
  data: {
    groups: [
      {
        id: 'api_keys',
        label: 'API keys',
        tools: [
          {
            name: 'list_my_keys',
            label: 'List API keys',
            description: 'View key settings.',
            effect: 'read_only',
            access: 'l1',
          },
          {
            name: 'create_my_key',
            label: 'Create an API key',
            description: 'Create a key after confirmation.',
            effect: 'confirmation',
            access: 'l1',
          },
        ],
      },
    ],
  },
}

test('legacy empty policy and missing maps retain the default enabled behavior', () => {
  for (const raw of [
    '',
    '   ',
    DEFAULT_ASSISTANT_TOOL_POLICY,
    '{"version":1}',
  ]) {
    const policy = parseAssistantToolPolicy(raw)
    assert.deepEqual(policy, { version: 1, groups: {}, tools: {} })
    assert.ok(policy)
    assert.equal(
      isAssistantToolEnabled(policy, 'api_keys', 'list_my_keys'),
      true
    )
  }
})

test('group disable wins over a true tool override and enabling restores saved individual choices', () => {
  const initial = parseAssistantToolPolicy(
    '{"version":1,"groups":{},"tools":{"list_my_keys":true,"create_my_key":false}}'
  )
  assert.ok(initial)
  const disabled = parseAssistantToolPolicy(
    updateAssistantToolPolicy(initial, 'groups', 'api_keys', false)
  )
  assert.ok(disabled)
  assert.equal(
    isAssistantToolEnabled(disabled, 'api_keys', 'list_my_keys'),
    false
  )
  assert.deepEqual(disabled.tools, initial.tools)
  const enabled = parseAssistantToolPolicy(
    updateAssistantToolPolicy(disabled, 'groups', 'api_keys', true)
  )
  assert.ok(enabled)
  assert.equal(
    isAssistantToolEnabled(enabled, 'api_keys', 'list_my_keys'),
    true
  )
  assert.equal(
    isAssistantToolEnabled(enabled, 'api_keys', 'create_my_key'),
    false
  )
  assert.deepEqual(enabled.tools, initial.tools)
  assert.deepEqual(initial.groups, {})
})

test('rejects malformed policies, duplicate or escaped duplicate keys and oversized bytes', () => {
  for (const raw of [
    'null',
    '[]',
    '{',
    '{"version":2}',
    '{"version":1,"groups":null}',
    '{"version":1,"tools":{"list_my_keys":"false"}}',
    '{"version":1,"unexpected":{}}',
    '{"version":1,"version":1}',
    '{"version":1,"tools":{"list_my_keys":true,"list_my_keys":false}}',
    '{"version":1,"tools":{"list_my_keys":true,"\\u006cist_my_keys":false}}',
    '{"version":1,"tools":{"__proto__":true}}',
    ' '.repeat(16_385),
    `{"version":1,"${'界'.repeat(6000)}":false}`,
  ]) {
    assert.equal(parseAssistantToolPolicy(raw), null, raw.slice(0, 120))
    assert.equal(assistantToolPolicySchema.safeParse(raw).success, false)
  }
})

test('requires a successful complete catalog and rejects ambiguous identifiers or metadata', () => {
  const groups = parseAssistantToolCatalog(catalogPayload)
  assert.ok(groups)
  assert.equal(groups[0].tools.length, 2)
  const malformed = [
    null,
    { data: catalogPayload.data },
    { ...catalogPayload, success: false },
    { success: true, data: { groups: {} } },
    {
      success: true,
      data: {
        groups: [catalogPayload.data.groups[0], catalogPayload.data.groups[0]],
      },
    },
    {
      success: true,
      data: {
        groups: [
          {
            ...catalogPayload.data.groups[0],
            tools: [
              catalogPayload.data.groups[0].tools[0],
              catalogPayload.data.groups[0].tools[0],
            ],
          },
        ],
      },
    },
    {
      success: true,
      data: {
        groups: [
          {
            ...catalogPayload.data.groups[0],
            tools: [
              { ...catalogPayload.data.groups[0].tools[0], access: 'public' },
            ],
          },
        ],
      },
    },
    {
      success: true,
      data: {
        groups: [
          {
            ...catalogPayload.data.groups[0],
            tools: [
              { ...catalogPayload.data.groups[0].tools[0], effect: 'write' },
            ],
          },
        ],
      },
    },
  ]
  for (const payload of malformed) {
    assert.equal(parseAssistantToolCatalog(payload), null)
  }
  assert.equal(
    assistantPolicyMatchesCatalog(
      { version: 1, groups: {}, tools: { list_my_keys: false } },
      groups
    ),
    true
  )
  assert.equal(
    assistantPolicyMatchesCatalog(
      { version: 1, groups: { unknown: false }, tools: {} },
      groups
    ),
    false
  )
  assert.equal(
    assistantPolicyMatchesCatalog(
      { version: 1, groups: {}, tools: { unknown: false } },
      groups
    ),
    false
  )
})
