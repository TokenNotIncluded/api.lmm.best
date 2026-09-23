/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { settingsShellTools } from './settings-shell'

const originalGet = api.get
const signal = () => new AbortController().signal
const calls: Array<{ to: string; search?: Record<string, unknown> }> = []
const tools = settingsShellTools({
  router: {
    navigate: async (value) => {
      calls.push(value)
    },
    subscribe: () => () => undefined,
  },
})
function tool(name: string) {
  const value = tools.find((item) => item.name === name)
  assert.ok(value)
  return value
}
function signIn(role: number) {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'operator', role })
}
afterEach(() => {
  api.get = originalGet
  calls.length = 0
  useAuthStore.getState().auth.setUser(null)
})

test('settings and system tools retain root-only access and remain read-only', async () => {
  assert.equal(new Set(tools.map((item) => item.name)).size, tools.length)
  for (const item of tools) {
    assert.equal(item.annotations?.readOnlyHint, true)
    assert.equal(item.annotations?.consequentialHint, undefined)
    await assert.rejects(
      item.execute({}, { signal: signal() }),
      /Sign in first/
    )
  }
  signIn(ROLE.ADMIN)
  for (const item of tools.filter(
    (entry) => !entry.name.startsWith('lmm_shell_')
  )) {
    await assert.rejects(
      item.execute({}, { signal: signal() }),
      /root administrator/
    )
  }
})

test('the settings catalog covers every category and only opens catalog destinations', async () => {
  signIn(ROLE.SUPER_ADMIN)
  const result = (await tool('lmm_settings_search').execute(
    {},
    { signal: signal() }
  )) as {
    sections: Array<{ group: string; title: string; path: string }>
  }
  for (const category of [
    'site',
    'auth',
    'billing',
    'models',
    'security',
    'content',
    'operations',
  ]) {
    assert.ok(
      result.sections.some((entry) =>
        entry.path.startsWith(`/system-settings/${category}/`)
      )
    )
  }
  const first = result.sections[0]
  assert.notEqual(first.title, first.group)
  await tool('lmm_settings_open').execute(
    { path: first.path },
    { signal: signal() }
  )
  assert.equal(calls[0].to, first.path)
  await assert.rejects(
    tool('lmm_settings_open').execute(
      { path: '/api/option/' },
      { signal: signal() }
    ),
    /Use a path/
  )
  await assert.rejects(
    tool('lmm_settings_open').execute(
      { path: 'https://elsewhere.test' },
      { signal: signal() }
    ),
    /Use a path/
  )
  assert.equal(calls.length, 1)
})

test('settings synonyms find the specific section', async () => {
  signIn(ROLE.SUPER_ADMIN)
  const result = (await tool('lmm_settings_search').execute(
    { query: 'smtp' },
    { signal: signal() }
  )) as {
    sections: Array<{ path: string }>
  }
  assert.deepEqual(
    result.sections.map((item) => item.path),
    ['/system-settings/operations/email']
  )
})

test('system results exclude arbitrary diagnostic payloads and credential-shaped fields', async () => {
  signIn(ROLE.SUPER_ADMIN)
  api.get = (async (url) => ({
    data: {
      success: true,
      data: url.includes('instances')
        ? [
            {
              status: 'online',
              instance_slot: 'a',
              last_seen_at: 100,
              started_at: 1,
              info: {
                runtime: { version: 'v1', password: 'SECRET' },
                env: { TOKEN: 'SECRET' },
                resources: {
                  cpu: { usage_percent: 15 },
                  memory: { usage_percent: Number.NaN },
                },
              },
            },
          ]
        : [
            {
              id: 4,
              type: 'log_cleanup',
              status: 'running',
              state: { progress: 25, secret: 'SECRET' },
              payload: { token: 'SECRET' },
              result: { key: 'SECRET' },
              error: 'SECRET',
              created_at: 1,
              updated_at: 2,
            },
          ],
    },
  })) as typeof api.get
  const instances = await tool('lmm_system_instances').execute(
    {},
    { signal: signal() }
  )
  const tasks = await tool('lmm_system_tasks').execute(
    { limit: 1 },
    { signal: signal() }
  )
  assert.doesNotMatch(
    JSON.stringify({ instances, tasks }),
    /SECRET|password|TOKEN|payload/
  )
  assert.match(JSON.stringify(instances), /"memory_percent":null/)
  assert.match(JSON.stringify(tasks), /"progress":25/)
})

test('cancellation prevents navigation and root access is rechecked after a fetch', async () => {
  signIn(ROLE.SUPER_ADMIN)
  const controller = new AbortController()
  controller.abort(new Error('cancelled'))
  await assert.rejects(
    tool('lmm_settings_open').execute(
      { path: '/system-info' },
      { signal: controller.signal }
    ),
    /cancelled/
  )
  assert.equal(calls.length, 0)
  api.get = (async () => {
    signIn(ROLE.USER)
    return { data: { success: true, data: [] } }
  }) as typeof api.get
  await assert.rejects(
    tool('lmm_system_instances').execute({}, { signal: signal() }),
    /administrator account/
  )
})
