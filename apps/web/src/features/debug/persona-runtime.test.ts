/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { after, describe, test } from 'node:test'

import { Window } from 'happy-dom'

const platformFetch = globalThis.fetch
const domWindow = new Window({ url: 'http://127.0.0.1:4174/' })
for (const key of [
  'window',
  'document',
  'navigator',
  'CustomEvent',
  'Event',
  'Request',
  'Response',
  'fetch',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { api } = await import('@/lib/http-client')
const { refreshAuthentication } = await import('@/lib/auth-session')
const { useAuthStore } = await import('@/stores/auth-store')
const {
  DEBUG_PERSONA_IDS,
  getActiveDebugPersona,
  installPersonaDebugRuntime,
  resetPersonaDebugRuntime,
  setActiveDebugPersona,
} = await import('./persona-runtime')

const originalAdapter = api.defaults.adapter

after(() => {
  api.defaults.adapter = originalAdapter
  globalThis.fetch = platformFetch
  useAuthStore.getState().auth.reset('idle')
  domWindow.close()
})

describe('persona debug runtime', () => {
  test('installs an isolated L0 bundle and switches complete personas', async () => {
    installPersonaDebugRuntime()

    assert.equal(getActiveDebugPersona(), 'l0')
    assert.equal(
      useAuthStore.getState().auth.user?.developer_access_granted,
      false
    )

    setActiveDebugPersona('admin')
    assert.equal(useAuthStore.getState().auth.user?.role, 100)
    assert.equal(useAuthStore.getState().auth.user?.trust_level_info?.level, 4)

    const status = await api.get('/api/assistant/status')
    assert.equal(status.data.data.is_root, true)
  })

  test('serves dynamic assistant starters without reaching a backend', async () => {
    installPersonaDebugRuntime()

    const presets = await api.get('/api/assistant/pre-conversation-presets')
    assert.equal(presets.data.success, true)
    assert.equal(presets.data.data.version, 'persona-fixture-v1')
    assert.equal(presets.data.data.presets.length, 4)
    assert.ok(
      presets.data.data.presets.every(
        (preset: { id?: string; prompt?: string }) =>
          Boolean(preset.id?.trim()) && Boolean(preset.prompt?.trim())
      )
    )

    const click = await api.post(
      '/api/assistant/pre-conversation-presets/models-and-pricing/click'
    )
    assert.deepEqual(click.data, { success: true, data: null })
  })

  test('serves the sidebar todo badge from the isolated fixture', async () => {
    installPersonaDebugRuntime()

    const todos = await api.get('/api/todos?category=all&p=1&page_size=50')
    assert.equal(todos.data.success, true)
    assert.equal(todos.data.data.total_unread_count, 0)
    assert.deepEqual(todos.data.data.items, [])
  })

  test('covers guided, normal, and operator fixtures across journey and gift routes', async () => {
    installPersonaDebugRuntime()
    assert.deepEqual(DEBUG_PERSONA_IDS, ['l0', 'b', 'e', 'f', 'l1', 'admin'])

    for (const fixture of [
      {
        id: 'b',
        username: 'debug_b_guided_buyer',
        access: false,
        trust: 0,
        group: 'default',
      },
      {
        id: 'e',
        username: 'debug_e_normal_user',
        access: true,
        trust: 1,
        group: 'default',
      },
      {
        id: 'f',
        username: 'debug_f_enterprise_operator',
        access: true,
        trust: 2,
        group: 'enterprise',
      },
    ] as const) {
      setActiveDebugPersona(fixture.id)

      const user = await api.get('/api/user/self')
      assert.equal(user.data.data.username, fixture.username)
      assert.equal(user.data.data.developer_access_granted, fixture.access)
      assert.equal(user.data.data.trust_level_info.level, fixture.trust)
      assert.equal(user.data.data.group, fixture.group)

      const groups = await api.get('/api/user/self/groups')
      assert.deepEqual(Object.keys(groups.data.data), [fixture.group])

      if (fixture.access) {
        assert.equal((await api.get('/api/token/')).status, 200)
      } else {
        assert.equal((await api.get('/api/token/')).status, 403)
      }

      const journey = await api.get('/api/assistant/journey')
      assert.equal(journey.data.success, true)
      assert.equal(journey.data.data.main.length, 6)
      assert.equal(journey.data.data.side.length, 2)
      assert.equal(
        journey.data.data.main.filter(
          (step: { status: string }) => step.status === 'pending'
        ).length,
        fixture.access ? 0 : 5
      )
      assert.equal(
        journey.data.data.side[0].status,
        fixture.id === 'b' ? 'pending' : 'completed'
      )
    }

    setActiveDebugPersona('b')
    const gift = await api.get('/api/assistant/new-user-gift')
    assert.equal(gift.data.data.status, 'offered')
    const claim = await api.post('/api/assistant/new-user-gift/claim')
    assert.equal(claim.data.data.gift.status, 'claimed')

    setActiveDebugPersona('e')
    assert.equal(
      (await api.get('/api/assistant/new-user-gift')).data.data,
      null
    )
    const history = await api.get('/api/assistant/conversations')
    assert.equal(history.data.data.conversations.length, 1)
  })

  test('returns lower-access conversation fixtures without raw secrets', async () => {
    setActiveDebugPersona('admin')
    const history = await api.get('/api/assistant/conversations', {
      params: { user_id: 1001 },
    })
    const serialized = JSON.stringify(history.data)

    assert.equal(history.data.data.conversations.length, 1)
    assert.match(serialized, /\[REDACTED:EMAIL\]/)
    assert.doesNotMatch(serialized, /@example\./)
  })

  test('applies the same hierarchy boundary to history lists and details', async () => {
    setActiveDebugPersona('l0')
    await assert.rejects(
      api.get('/api/assistant/conversations', { params: { user_id: 1002 } }),
      (error: unknown) =>
        (error as { response?: { status?: number } }).response?.status === 404
    )
    await assert.rejects(
      api.get('/api/assistant/conversations/8102'),
      (error: unknown) =>
        (error as { response?: { status?: number } }).response?.status === 404
    )

    setActiveDebugPersona('l1')
    const lowerAccess = await api.get('/api/assistant/conversations', {
      params: { user_id: 1001 },
    })
    assert.equal(lowerAccess.data.data.conversations.length, 1)
    assert.equal(
      (await api.get('/api/assistant/conversations/8101')).status,
      200
    )
    await assert.rejects(
      api.get('/api/assistant/conversations', { params: { user_id: 1099 } }),
      (error: unknown) =>
        (error as { response?: { status?: number } }).response?.status === 404
    )

    setActiveDebugPersona('admin')
    assert.equal(
      (
        await api.get('/api/assistant/conversations', {
          params: { user_id: 1001 },
        })
      ).status,
      200
    )
  })

  test('keeps auth refresh on the isolated adapter', async () => {
    setActiveDebugPersona('l1')
    useAuthStore.getState().auth.reset('idle')
    const outcome = await refreshAuthentication()

    assert.equal(outcome.kind, 'authenticated')
    assert.equal(useAuthStore.getState().auth.session?.sid, 'debug-persona-l1')
  })

  test('saves only non-sensitive preview preferences without changing account grants', async () => {
    setActiveDebugPersona('l1')
    const before = (await api.get('/api/user/self')).data.data
    await api.put('/api/user/self', { language: 'zh', sidebar_modules: '{}' })
    const after = (await api.get('/api/user/self')).data.data
    assert.equal(after.language, 'zh')
    assert.equal(after.sidebar_modules, '{}')
    assert.equal(after.quota, before.quota)
    assert.equal(
      after.developer_access_granted,
      before.developer_access_granted
    )
    await assert.rejects(
      api.put(
        '/api/user/self',
        { quota: 999999999 },
        { skipErrorHandler: true }
      ),
      /only saves language and sidebar preferences locally/
    )
    resetPersonaDebugRuntime()
    setActiveDebugPersona('l1')
    assert.equal(
      (await api.get('/api/user/self')).data.data.language,
      undefined
    )
  })

  test('blocks unmocked axios and fetch API traffic', async () => {
    await assert.rejects(
      api.post('/api/unsafe-production-mutation', { enabled: true }),
      /PERSONA_DEBUG_UNMOCKED_REQUEST/
    )
    await assert.rejects(
      fetch('/api/unsafe-production-mutation'),
      /PERSONA_DEBUG_UNMOCKED_REQUEST/
    )
    await assert.rejects(fetch('/api'), /PERSONA_DEBUG_UNMOCKED_REQUEST/)
    await assert.rejects(fetch('/mj/task'), /PERSONA_DEBUG_UNMOCKED_REQUEST/)
    await assert.rejects(fetch('/pg/task'), /PERSONA_DEBUG_UNMOCKED_REQUEST/)
    await assert.rejects(
      fetch('https://example.com/api/status'),
      /PERSONA_DEBUG_EXTERNAL_REQUEST/
    )

    const status = await fetch('/api/status')
    assert.equal(status.status, 200)
    assert.equal((await status.json()).data.system_name, 'LMM Persona Lab')
  })

  test('reset restores the deterministic L0 fixture', () => {
    setActiveDebugPersona('l1')
    resetPersonaDebugRuntime()

    assert.equal(getActiveDebugPersona(), 'l0')
    assert.equal(useAuthStore.getState().auth.user?.id, 1001)
  })
})
