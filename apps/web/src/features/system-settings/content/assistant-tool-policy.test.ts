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
  defaultAssistantToolRule,
  assistantToolRule,
  updateAssistantToolRule,
  weeklyDiscountLimit,
  mergeAssistantToolPolicy,
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

test('level defaults express inclusive minimums without granting administrator roles', () => {
  const group = parseAssistantToolCatalog(catalogPayload)![0]
  assert.deepEqual(defaultAssistantToolRule(group.tools[0]), {
    min_level: 1,
    max_level: 6,
  })
  assert.deepEqual(
    defaultAssistantToolRule({ ...group.tools[0], access: 'admin' }),
    { min_level: 5, max_level: 6 }
  )
  assert.deepEqual(
    defaultAssistantToolRule({ ...group.tools[0], access: 'root' }),
    { min_level: 6, max_level: 6 }
  )
  const policy = parseAssistantToolPolicy(DEFAULT_ASSISTANT_TOOL_POLICY)!
  const configured = parseAssistantToolPolicy(
    updateAssistantToolRule(policy, 'list_my_keys', {
      min_level: 2,
      max_level: 4,
    })
  )!
  assert.deepEqual(assistantToolRule(configured, group.tools[0]), {
    min_level: 2,
    max_level: 4,
  })
  assert.equal(assistantPolicyMatchesCatalog(configured, [group]), true)
  const tooLow = {
    ...configured,
    rules: { list_my_keys: { min_level: 0, max_level: 6 } },
  }
  assert.equal(assistantPolicyMatchesCatalog(tooLow, [group]), false)
  assert.deepEqual(
    parseAssistantToolPolicy(
      updateAssistantToolRule(configured, 'list_my_keys')
    ),
    policy
  )
})

test('discount, market and issue policies reject unsafe values instead of enabling defaults', () => {
  const make = (name: string, rule: unknown) =>
    JSON.stringify({ version: 1, rules: { [name]: rule } })
  const bounds = { min_level: 1, max_level: 6 }
  assert.equal(weeklyDiscountLimit(bounds, 1), 10)
  assert.equal(weeklyDiscountLimit(bounds, 5), 0)
  for (const raw of [
    make('prepare_weekly_discount', {
      ...bounds,
      discount_percent_by_level: { '1': 100 },
    }),
    make('prepare_weekly_discount', {
      ...bounds,
      discount_percent_by_level: { '5': 1 },
    }),
    make('calculate_math', {
      ...bounds,
      discount_percent_by_level: { '1': 20 },
    }),
    make('call_market_tool', { ...bounds, market_service_ids: ['a', 'a'] }),
    make('call_market_tool', { ...bounds, market_service_ids: null }),
    make('create_site_issue', { ...bounds, default_visibility: 'public' }),
  ]) {
    assert.equal(parseAssistantToolPolicy(raw), null, raw)
  }
  const raw = make('prepare_weekly_discount', {
    ...bounds,
    discount_percent_by_level: { '1': 25 },
  })
  assert.equal(
    weeklyDiscountLimit(
      parseAssistantToolPolicy(raw)!.rules!.prepare_weekly_discount,
      1
    ),
    25
  )
})

test('concurrent rule edits intersect access and keep the lower reward cap', () => {
  const encode = (rules: unknown) =>
    JSON.stringify({ version: 1, groups: {}, tools: {}, rules })
  const before = encode({
    prepare_weekly_discount: { min_level: 0, max_level: 6 },
  })
  const local = encode({
    prepare_weekly_discount: {
      min_level: 2,
      max_level: 6,
      discount_percent_by_level: { '2': 25 },
    },
  })
  const remote = encode({
    prepare_weekly_discount: {
      min_level: 0,
      max_level: 4,
      discount_percent_by_level: { '2': 15 },
    },
  })
  const merged = parseAssistantToolPolicy(
    mergeAssistantToolPolicy(before, local, remote)
  )!
  assert.equal(merged.rules!.prepare_weekly_discount.min_level, 2)
  assert.equal(merged.rules!.prepare_weekly_discount.max_level, 4)
  assert.equal(
    weeklyDiscountLimit(merged.rules!.prepare_weekly_discount, 2),
    15
  )
  const market = (ids: string[]) =>
    encode({
      call_market_tool: { min_level: 1, max_level: 6, market_service_ids: ids },
    })
  const linked = parseAssistantToolPolicy(
    mergeAssistantToolPolicy(
      market(['a']),
      market(['a', 'b']),
      market(['a', 'c'])
    )
  )!
  assert.deepEqual(linked.rules!.call_market_tool.market_service_ids, ['a'])
})
