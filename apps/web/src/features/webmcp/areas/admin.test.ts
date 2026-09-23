/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { useAuthStore } from '@/stores/auth-store'

import type { ModelContextTool, WebMcpRouter } from '../tool-kit'
import {
  adminTools,
  maskAdminCode,
  maskAdminEmail,
  summarizeChannelHealth,
  toChannelRow,
} from './admin'

const READ_ONLY_TOOLS = [
  'lmm_admin_channels_list',
  'lmm_admin_channels_health',
  'lmm_admin_users_search',
  'lmm_admin_redemptions_list',
  'lmm_admin_discount_codes_list',
  'lmm_admin_activations_list',
  'lmm_admin_acquisition_summary',
  'lmm_admin_system_info',
  'lmm_admin_company_profile',
  'lmm_admin_open',
] as const

function buildTools() {
  const calls: Array<{ to: string; search?: Record<string, unknown> }> = []
  const router: WebMcpRouter = {
    navigate: async (options: {
      to: string
      search?: Record<string, unknown>
    }) => {
      calls.push(options)
      return undefined
    },
    subscribe: () => () => undefined,
  }
  return { tools: adminTools({ router }), calls }
}

function byName(tools: readonly ModelContextTool[], name: string) {
  const tool = tools.find((entry) => entry.name === name)
  assert.ok(tool, `${name} is registered`)
  return tool
}

function signInAs(role: number) {
  useAuthStore.setState({
    auth: {
      ...useAuthStore.getState().auth,
      user: { id: 1, role } as never,
    } as never,
  })
}

const signal = () => new AbortController().signal

test('every admin tool is uniquely named and read-only', () => {
  const { tools } = buildTools()
  assert.equal(tools.length, READ_ONLY_TOOLS.length)
  const names = tools.map((tool) => tool.name)
  assert.equal(new Set(names).size, names.length)
  for (const name of READ_ONLY_TOOLS) {
    assert.ok(names.includes(name), `${name} is registered`)
  }
  for (const tool of tools) {
    assert.match(tool.name, /^lmm_admin_[a-z0-9_]+$/)
    assert.equal(tool.annotations?.readOnlyHint, true, tool.name)
    // Administrator tools must never advertise a destructive write.
    assert.equal(tool.annotations?.consequentialHint, undefined, tool.name)
    assert.ok((tool.title ?? '').length > 0, `${tool.name} has a title`)
    assert.ok(tool.description.length > 0, `${tool.name} has a description`)
  }
})

test('every admin tool refuses non-administrators', async () => {
  signInAs(1)
  const { tools } = buildTools()
  for (const name of READ_ONLY_TOOLS) {
    await assert.rejects(
      byName(tools, name).execute({ page: '/channels' }, { signal: signal() }),
      /administrator account/,
      `${name} refuses a normal user`
    )
  }
})

test('masked emails keep the domain but never the local part', () => {
  assert.equal(maskAdminEmail('alice@example.com'), 'a••••@example.com')
  assert.equal(maskAdminEmail('a@example.com'), 'a•••@example.com')
  assert.equal(maskAdminEmail('notanemail'), 'n•••')
  assert.equal(maskAdminEmail(''), null)
  assert.equal(maskAdminEmail(null), null)
})

test('masked codes never contain the full code', () => {
  const code = 'sk-live-abcdef123456'
  const masked = maskAdminCode(code)
  assert.ok(masked)
  assert.notEqual(masked, code)
  assert.ok(!masked.includes('abcdef123456'))
  assert.equal(maskAdminCode('1234'), '••••')
  assert.equal(maskAdminCode(''), null)
})

test('channel rows drop key, base_url, headers and mappings', () => {
  const row = toChannelRow({
    id: 7,
    name: 'primary upstream',
    type: 1,
    status: 1,
    response_time: 320,
    balance: 12.5,
    used_quota: 100,
    group: 'default, vip',
    tag: 'prod',
    priority: 10,
    weight: 5,
    test_time: 1_700_000_000,
    models: 'gpt-4o, gpt-4o-mini',
    // Fields below must never reach the result.
    key: 'sk-secret',
    base_url: 'https://user:pass@upstream.example',
  } as never)
  const serialized = JSON.stringify(row)
  assert.ok(!serialized.includes('sk-secret'))
  assert.ok(!serialized.includes('upstream.example'))
  assert.ok(!serialized.includes('pass'))
  assert.deepEqual(row.groups, ['default', 'vip'])
  assert.equal(row.model_count, 2)
  assert.equal(row.type_label, 'OpenAI')
})

test('channel health counts status, tests, slowness and balance', () => {
  const rows = [
    toChannelRow({
      id: 1,
      name: 'a',
      type: 1,
      status: 1,
      response_time: 200,
      balance: 5,
      used_quota: 0,
      group: 'default',
      test_time: 1,
      models: '',
    } as never),
    toChannelRow({
      id: 2,
      name: 'b',
      type: 1,
      status: 3,
      response_time: 0,
      balance: 0,
      used_quota: 0,
      group: 'default',
      test_time: 0,
      models: '',
    } as never),
    toChannelRow({
      id: 3,
      name: 'c',
      type: 1,
      status: 2,
      response_time: 9000,
      balance: -1,
      used_quota: 0,
      group: 'default',
      test_time: 2,
      models: '',
    } as never),
  ]
  const health = summarizeChannelHealth(rows, 120, { '1': 120 }, 3)
  assert.equal(health.sampled, 3)
  assert.equal(health.total, 120)
  assert.equal(health.enabled, 1)
  assert.equal(health.auto_disabled, 1)
  assert.equal(health.manual_disabled, 1)
  assert.equal(health.never_tested, 1)
  assert.equal(health.slow_over_5s, 1)
  assert.equal(health.zero_balance, 2)
  assert.equal(health.average_response_time_ms, 4600)
  assert.equal(health.slowest[0].id, 3)
  assert.equal(health.retry_times, 3)
  assert.deepEqual(health.type_counts, { '1': 120 })
})

test('the open tool navigates with validated channel filters', async () => {
  signInAs(100)
  const { tools, calls } = buildTools()
  const result = (await byName(tools, 'lmm_admin_open').execute(
    {
      page: '/channels',
      keyword: 'openai',
      status: 'enabled',
      type: 3,
      group: 'vip',
      page_number: 2,
    },
    { signal: signal() }
  )) as { navigated: boolean }
  assert.equal(result.navigated, true)
  assert.equal(calls.length, 1)
  assert.equal(calls[0].to, '/channels')
  assert.deepEqual(calls[0].search, {
    filter: 'openai',
    status: ['enabled'],
    type: ['3'],
    group: ['vip'],
    page: 2,
  })
})

test('the open tool rejects an unknown page or an empty filter set', async () => {
  signInAs(100)
  const { tools, calls } = buildTools()
  await assert.rejects(
    byName(tools, 'lmm_admin_open').execute(
      { page: '/nope' },
      { signal: signal() }
    ),
    TypeError
  )
  const result = (await byName(tools, 'lmm_admin_open').execute(
    // A page without filter support still navigates, with no search payload.
    { page: '/system-info' },
    { signal: signal() }
  )) as { filters: unknown }
  assert.equal(result.filters, null)
  assert.equal(calls.length, 1)
  assert.equal(calls[0].search, undefined)
})
