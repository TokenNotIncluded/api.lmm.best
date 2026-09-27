/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import type { ModelContextTool, WebMcpRouter } from '../tool-kit'
import { workbenchTools } from './workbench'

function makeRouter() {
  const calls: Array<{ to: string; search?: Record<string, unknown> }> = []
  const router: WebMcpRouter = {
    navigate: async (options) => {
      calls.push(options)
      return undefined
    },
    subscribe: () => () => undefined,
  }
  return { router, calls }
}

function buildTools() {
  const { router, calls } = makeRouter()
  return { tools: workbenchTools({ router }), calls }
}

function byName(tools: readonly ModelContextTool[], name: string) {
  const tool = tools.find((entry) => entry.name === name)
  assert.ok(tool, `${name} is registered`)
  return tool
}

function signIn(role: number = ROLE.USER) {
  useAuthStore.setState({
    auth: {
      ...useAuthStore.getState().auth,
      user: { id: 1, role },
    } as never,
  })
}

function signOut() {
  useAuthStore.setState({
    auth: { ...useAuthStore.getState().auth, user: null } as never,
  })
}

const signal = () => new AbortController().signal

test('every workbench tool is uniquely named under the lmm_workbench area', () => {
  const { tools } = buildTools()
  assert.ok(tools.length > 0)
  const names = tools.map((tool) => tool.name)
  assert.equal(new Set(names).size, names.length)
  for (const name of names) {
    assert.match(
      name,
      /^lmm_workbench_[a-z0-9_]+$/,
      `${name} follows the area convention`
    )
  }
  for (const tool of tools) {
    assert.equal(typeof tool.title, 'string')
    assert.ok(
      (tool.title ?? '').length > 0,
      `${tool.name} has a translatable title`
    )
    assert.ok(tool.description.length > 0)
    assert.equal(typeof tool.execute, 'function')
  }
})

test('every navigation tool starts with lmm_workbench_open_', () => {
  const { tools } = buildTools()
  const opening = tools.filter((tool) => tool.name.includes('_open_'))
  assert.deepEqual(opening.map((tool) => tool.name).sort(), [
    'lmm_workbench_open_bounty',
    'lmm_workbench_open_drawing',
    'lmm_workbench_open_models_section',
    'lmm_workbench_open_playground',
    'lmm_workbench_open_support_tickets',
    'lmm_workbench_open_tool_market',
    'lmm_workbench_open_workspace',
  ])
})

test('write tools refuse without an explicit confirm and carry consequentialHint', async () => {
  signIn()
  const { tools } = buildTools()
  const writes = tools.filter((tool) =>
    tool.name.startsWith('lmm_workbench_mark_')
  )
  assert.ok(writes.length > 0)
  for (const tool of writes) {
    assert.equal(tool.annotations?.consequentialHint, true, tool.name)
    assert.equal(tool.annotations?.readOnlyHint, undefined, tool.name)
    await assert.rejects(
      tool.execute({ category: 'all', source_id: 7 }, { signal: signal() }),
      /Refused/,
      `${tool.name} refuses without confirm`
    )
  }
})

test('read tools declare readOnlyHint and never consequentialHint', () => {
  const { tools } = buildTools()
  const reads = tools.filter((tool) =>
    [
      'lmm_workbench_list_api_keys',
      'lmm_workbench_list_models',
      'lmm_workbench_list_deployments',
      'lmm_workbench_list_todos',
      'lmm_workbench_list_tool_market',
      'lmm_workbench_list_public_relays',
      'lmm_workbench_list_bounties',
      'lmm_workbench_list_remote_sessions',
      'lmm_workbench_image_prompt_ideas',
      'lmm_workbench_public_relay_routing',
      'lmm_workbench_bounty_detail',
    ].includes(tool.name)
  )
  assert.equal(reads.length, 11)
  for (const tool of reads) {
    assert.equal(tool.annotations?.readOnlyHint, true, tool.name)
    assert.equal(tool.annotations?.consequentialHint, undefined, tool.name)
  }
})

test('user-generated content tools carry untrustedContentHint', () => {
  const { tools } = buildTools()
  for (const name of [
    'lmm_workbench_list_todos',
    'lmm_workbench_list_tool_market',
    'lmm_workbench_list_public_relays',
    'lmm_workbench_list_bounties',
    'lmm_workbench_public_relay_routing',
    'lmm_workbench_bounty_detail',
  ]) {
    assert.equal(
      byName(tools, name).annotations?.untrustedContentHint,
      true,
      name
    )
  }
})

test('signed-out accounts cannot run any workbench tool', async () => {
  signOut()
  const { tools } = buildTools()
  for (const tool of tools) {
    await assert.rejects(
      tool.execute({}, { signal: signal() }),
      /Sign in first/,
      `${tool.name} requires a signed-in account`
    )
  }
})

test('models section navigation only accepts known sections', async () => {
  signIn(ROLE.ADMIN)
  const { tools, calls } = buildTools()
  const tool = byName(tools, 'lmm_workbench_open_models_section')
  await assert.rejects(
    tool.execute({ section: 'secrets' }, { signal: signal() }),
    /section must be one of/
  )
  assert.equal(calls.length, 0)
  assert.deepEqual(
    await tool.execute({ section: 'deployments' }, { signal: signal() }),
    { section: 'deployments', note: 'Administrator accounts only.' }
  )
  assert.deepEqual(calls.at(-1), { to: '/models/deployments' })
})

test('playground tool routes to /playground or a concrete chat', async () => {
  signIn()
  const { tools, calls } = buildTools()
  const tool = byName(tools, 'lmm_workbench_open_playground')
  await tool.execute({}, { signal: signal() })
  assert.equal(calls.at(-1)?.to, '/playground')
  await tool.execute({ chat_id: 42 }, { signal: signal() })
  assert.equal(calls.at(-1)?.to, '/chat/42')
})

test('admin-only workbench tools refuse a signed-in ordinary account', async () => {
  signIn(ROLE.USER)
  const { tools, calls } = buildTools()
  const adminOnly = [
    'lmm_workbench_list_models',
    'lmm_workbench_list_deployments',
    'lmm_workbench_open_models_section',
  ]
  for (const name of adminOnly) {
    await assert.rejects(
      byName(tools, name).execute(
        { section: 'metadata' },
        { signal: signal() }
      ),
      /administrator account/,
      `${name} is admin-gated`
    )
  }
  assert.equal(calls.length, 0)
})

test('marking one to-do read needs a concrete category and source id', async () => {
  signIn()
  const { tools } = buildTools()
  const tool = byName(tools, 'lmm_workbench_mark_todo_read')
  await assert.rejects(
    tool.execute(
      { category: 'all', source_id: 3, confirm: true },
      { signal: signal() }
    ),
    /category must name one concrete category/
  )
  await assert.rejects(
    tool.execute(
      { category: 'human_support', confirm: true },
      { signal: signal() }
    ),
    /source_id is required/
  )
})

test('the key list never echoes a full key, only a mask', async () => {
  signIn()
  const { tools } = buildTools()
  const fullKey = 'abcdefghijklmnopqrstuvwxyz0123456789'
  const original = api.get
  api.get = (async () => ({
    data: {
      success: true,
      data: {
        items: [
          {
            id: 9,
            name: 'prod',
            key: fullKey,
            status: 1,
            remain_quota: 100,
            used_quota: 5,
            unlimited_quota: false,
            expired_time: -1,
            group: 'default',
          },
        ],
        total: 1,
      },
    },
  })) as typeof api.get
  try {
    const result = (await byName(tools, 'lmm_workbench_list_api_keys').execute(
      {},
      { signal: signal() }
    )) as { keys: Array<Record<string, unknown>> }
    const serialized = JSON.stringify(result)
    assert.ok(!serialized.includes(fullKey), 'the raw key never appears')
    assert.match(String(result.keys[0]?.masked_key), /•/)
    assert.equal(result.keys[0]?.never_expires, true)
  } finally {
    api.get = original
  }
})

test('relay routing and bounty detail are GET-only and drop contributor emails', async () => {
  signIn()
  const { tools } = buildTools()
  const email = 'contributor@example.com'
  const requested: string[] = []
  const original = api.get
  api.get = (async (url: string) => {
    requested.push(url)
    if (url === '/api/public-relays/routing') {
      return {
        data: {
          success: true,
          data: {
            group: 'public',
            items: [
              {
                id: 1,
                position: 0,
                disabled: false,
                name: 'a',
                contributor_email: email,
              },
              {
                id: 2,
                position: 1,
                disabled: true,
                name: 'b',
                contributor_email: email,
              },
            ],
          },
        },
      }
    }
    return {
      data: {
        success: true,
        data: {
          project: { id: 4, title: 'fix it', reward_quota: 10 },
          challenges: [{ id: 8, status: 'submitted', github_handle: 'dev' }],
          ledger: [{ id: 1 }, { id: 2 }],
        },
      },
    }
  }) as typeof api.get
  try {
    const routing = (await byName(
      tools,
      'lmm_workbench_public_relay_routing'
    ).execute({}, { signal: signal() })) as Record<string, unknown>
    assert.ok(!JSON.stringify(routing).includes(email))
    assert.equal(routing.count, 2)
    assert.equal(routing.enabled_count, 1)

    const detail = (await byName(tools, 'lmm_workbench_bounty_detail').execute(
      { project_id: 4 },
      { signal: signal() }
    )) as Record<string, unknown>
    assert.equal(detail.challenge_count, 1)
    assert.equal(detail.ledger_entries, 2)
    assert.deepEqual(requested, [
      '/api/public-relays/routing',
      '/api/open-source-bounties/projects/4',
    ])
  } finally {
    api.get = original
  }
})

test('bounty navigation passes numeric search params the route schema accepts', async () => {
  signIn()
  const { tools, calls } = buildTools()
  const tool = byName(tools, 'lmm_workbench_open_bounty')
  await assert.rejects(
    tool.execute({}, { signal: signal() }),
    /project_id is required/
  )
  await tool.execute({ project_id: 12, challenge_id: 7 }, { signal: signal() })
  const call = calls.at(-1)
  assert.ok(call)
  assert.equal(call.to, '/open-source-bounties')
  assert.deepEqual(call.search, { projectId: 12, challengeId: 7 })
  // The route schema is z.number(), so these must not be strings.
  assert.equal(typeof call.search?.projectId, 'number')
})

test('support navigation only forwards a known category and a reference id', async () => {
  signIn()
  const { tools, calls } = buildTools()
  const tool = byName(tools, 'lmm_workbench_open_support_tickets')
  await assert.rejects(
    tool.execute({ category: 'unknown' }, { signal: signal() }),
    /category must be one of/
  )
  assert.equal(calls.length, 0)
  await tool.execute(
    { category: 'technical', reference_id: 'CH-9' },
    { signal: signal() }
  )
  assert.deepEqual(calls.at(-1)?.search, {
    category: 'technical',
    referenceId: 'CH-9',
  })
})

test('aborted signals stop a tool before it navigates', async () => {
  signIn()
  const { tools, calls } = buildTools()
  const controller = new AbortController()
  controller.abort()
  await assert.rejects(
    byName(tools, 'lmm_workbench_open_workspace').execute(
      {},
      { signal: controller.signal }
    )
  )
  assert.equal(calls.length, 0)
})
