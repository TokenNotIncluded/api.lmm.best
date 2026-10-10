/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  UI_PREFERENCE_OPTIONS,
  parseAssistantUIPreferenceAction,
  parseUIPreferencePatch,
  uiPreferenceUndoPatch,
  type AssistantUIPreferenceAction,
  type UIPreferencePatch,
  type UIPreferences,
} from './assistant-ui-preferences-contract'
import { createUIPreferenceRuntime, type UIPreferenceAdapter, type UIPreferenceOwner } from './assistant-ui-preferences-runtime'

function fixture() {
  let time = 1_000_000
  let serial = 0
  let owner: UIPreferenceOwner | undefined = { userID: 7, sessionID: 'session-a' }
  let current: UIPreferences = { mode: 'system', theme: 'default', language: 'en', currency: 'auto' }
  const writes: UIPreferencePatch[] = []
  const previews: UIPreferencePatch[] = []
  let releases = 0
  const timers: Array<{ delay: number; cancelled: boolean; run: () => void }> = []
  const runtime = createUIPreferenceRuntime(() => time, (run, delay) => {
    const timer = { run, delay, cancelled: false }
    timers.push(timer)
    return () => { timer.cancelled = true }
  })
  const adapter: UIPreferenceAdapter = {
    owner: () => owner,
    read: () => ({ ...current }),
    save: async (patch, signal) => {
      assert.equal(signal.aborted, false)
      writes.push({ ...patch })
      current = { ...current, ...patch }
    },
    preview: (patch) => {
      previews.push({ ...patch })
      return () => { releases++ }
    },
  }
  const action = (preview: UIPreferencePatch = { mode: 'dark' }, temporary = false): AssistantUIPreferenceAction => ({
    type: 'workspace_action', tool: 'set_ui_preferences', requires_confirmation: false,
    action_id: (++serial).toString(16).padStart(32, '0'), actor_user_id: 7,
    actor_session_id: 'session-a', expires_at: Math.floor(time / 1000) + 120,
    temporary, preview,
  })
  return {
    runtime, adapter, action, writes, previews, timers,
    signal: new AbortController(),
    current: () => current,
    releases: () => releases,
    manual: (patch: UIPreferencePatch) => { current = { ...current, ...patch } },
    setOwner: (next: UIPreferenceOwner | undefined) => { owner = next },
    advance: (ms: number) => { time += ms },
  }
}

test('all listed preference values parse without inserting omitted fields', () => {
  for (const [key, values] of Object.entries(UI_PREFERENCE_OPTIONS)) {
    for (const selected of values) {
      const patch = { [key]: selected }
      assert.deepEqual(parseUIPreferencePatch(patch), patch)
    }
  }
  for (const value of [null, [], { mode: null }, { currency: 'EUR' }, { theme: '<script>' }, { user_id: 1 }, { language: 'zh' }, { mode: '' }]) {
    assert.equal(parseUIPreferencePatch(value), undefined)
  }
})

test('only exact, bounded, session-bound action envelopes are accepted', () => {
  const f = fixture()
  const good = f.action({ mode: 'dark', theme: 'ocean-breeze' }, true)
  assert.deepEqual(parseAssistantUIPreferenceAction(good), good)
  for (const value of [
    null, [], {},
    { ...good, mode: 'light' },
    { ...good, requires_confirmation: true },
    { ...good, actor_user_id: 0 },
    { ...good, actor_user_id: 1.2 },
    { ...good, actor_session_id: '' },
    { ...good, action_id: 'not-an-id' },
    { ...good, expires_at: Infinity },
    { ...good, temporary: 'true' },
    { ...good, preview: {} },
    { ...good, preview: { language: 'ja' } },
    { ...good, preview: { currency: 'USD' } },
    { ...good, preview: { mode: 'dark', balance: 1000 } },
    { ...good, tool: 'restore_ui_preferences' },
  ]) assert.equal(parseAssistantUIPreferenceAction(value), undefined)
  assert.ok(parseAssistantUIPreferenceAction({ ...good, tool: 'restore_ui_preferences', temporary: false, preview: {} }))
})

test('a partial change saves once, reports success, and does not replay', async () => {
  const f = fixture()
  const action = f.action({ mode: 'dark', currency: 'CNY' })
  assert.equal((await f.runtime.run(action, f.adapter, f.signal.signal)).status, 'applied')
  await f.runtime.run(action, f.adapter, f.signal.signal)
  assert.deepEqual(f.writes, [{ mode: 'dark', currency: 'CNY' }])
  assert.deepEqual(f.current(), { mode: 'dark', currency: 'CNY', language: 'en', theme: 'default' })
})

test('expired, future, aborted and foreign-session actions never write', async () => {
  for (const kind of ['expired', 'future', 'user', 'session', 'aborted']) {
    const f = fixture()
    const action = f.action()
    if (kind === 'expired') action.expires_at -= 121
    if (kind === 'future') action.expires_at += 1000
    if (kind === 'user') action.actor_user_id = 8
    if (kind === 'session') action.actor_session_id = 'session-b'
    if (kind === 'aborted') f.signal.abort()
    assert.equal((await f.runtime.run(action, f.adapter, f.signal.signal)).status, 'unavailable')
    assert.equal(f.writes.length, 0)
  }
})

test('a saved receipt is not reported as applied to a different account', async () => {
  const f = fixture()
  const action = f.action()
  await f.runtime.run(action, f.adapter, f.signal.signal)
  f.setOwner({ userID: 8, sessionID: 'session-b' })
  assert.equal((await f.runtime.run(action, f.adapter, f.signal.signal)).status, 'unavailable')
  assert.equal(f.writes.length, 1)
})

test('save failures are visible and uncertain writes are not retried', async () => {
  const f = fixture()
  let attempts = 0
  f.adapter.save = async () => { attempts++; throw new Error('network failure') }
  const action = f.action()
  assert.equal((await f.runtime.run(action, f.adapter, f.signal.signal)).status, 'failed')
  assert.equal((await f.runtime.run(action, f.adapter, f.signal.signal)).status, 'failed')
  assert.equal(attempts, 1)
  assert.equal(f.runtime.receipt(action.action_id)?.canUndo, false)
})

test('temporary appearance previews never save and expire after eight seconds', async () => {
  const f = fixture()
  const action = f.action({ mode: 'dark', theme: 'rose-garden' }, true)
  assert.equal((await f.runtime.run(action, f.adapter, f.signal.signal)).status, 'previewing')
  await f.runtime.run(action, f.adapter, f.signal.signal)
  assert.equal(f.previews.length, 1)
  assert.equal(f.writes.length, 0)
  assert.equal(f.current().theme, 'default')
  assert.equal(f.timers[0]?.delay, 8000)
  f.timers[0]?.run()
  f.signal.abort()
  assert.equal(f.releases(), 1)
  assert.equal(f.runtime.receipt(action.action_id)?.status, 'restored')
})

test('closing a preview releases it and rapid repeated previews are blocked', async () => {
  const f = fixture()
  await f.runtime.run(f.action({ mode: 'dark' }, true), f.adapter, f.signal.signal)
  f.signal.abort()
  assert.equal(f.releases(), 1)
  assert.equal(f.timers[0]?.cancelled, true)
  assert.equal((await f.runtime.run(f.action({ theme: 'ocean-breeze' }, true), f.adapter, new AbortController().signal)).status, 'unavailable')
  assert.equal(f.previews.length, 1)
  assert.equal(f.writes.length, 0)
})

test('undo restores only the assistant fields and preserves later manual choices', async () => {
  const f = fixture()
  const action = f.action({ mode: 'dark', theme: 'rose-garden', currency: 'USD' })
  await f.runtime.run(action, f.adapter, f.signal.signal)
  f.manual({ theme: 'ocean-breeze', language: 'ja' })
  assert.equal((await f.runtime.undo(f.adapter, f.signal.signal, action.action_id)).status, 'restored')
  assert.deepEqual(f.writes[1], { mode: 'system', currency: 'auto' })
  assert.deepEqual(f.current(), { mode: 'system', theme: 'ocean-breeze', language: 'ja', currency: 'auto' })
  assert.equal((await f.runtime.undo(f.adapter, f.signal.signal, action.action_id)).status, 'unchanged')
})

test('an older Undo button cannot undo a newer assistant change', async () => {
  const f = fixture()
  const first = f.action({ mode: 'dark' })
  const second = f.action({ currency: 'CNY' })
  await f.runtime.run(first, f.adapter, f.signal.signal)
  await f.runtime.run(second, f.adapter, f.signal.signal)
  assert.equal((await f.runtime.undo(f.adapter, f.signal.signal, first.action_id)).status, 'unchanged')
  assert.equal(f.current().currency, 'CNY')
  const restore = { ...f.action({}), tool: 'restore_ui_preferences' } as const
  assert.equal((await f.runtime.run(restore, f.adapter, f.signal.signal)).status, 'restored')
  assert.equal(f.current().currency, 'auto')
  assert.equal(f.current().mode, 'dark')
})

test('an account change blocks undo and completion from an old session', async () => {
  const f = fixture()
  await f.runtime.run(f.action(), f.adapter, f.signal.signal)
  f.setOwner(undefined)
  assert.equal((await f.runtime.undo(f.adapter, f.signal.signal)).status, 'unchanged')
  assert.equal(f.writes.length, 1)
  const other = fixture()
  other.adapter.save = async () => { other.setOwner(undefined) }
  assert.equal((await other.runtime.run(other.action(), other.adapter, other.signal.signal)).status, 'failed')
})

test('a pending save does not allow a second mutation to overtake it', async () => {
  const f = fixture()
  let finish!: () => void
  f.adapter.save = () => new Promise<void>((resolve) => { finish = resolve })
  const first = f.runtime.run(f.action(), f.adapter, f.signal.signal)
  assert.equal((await f.runtime.run(f.action({ currency: 'CNY' }), f.adapter, f.signal.signal)).status, 'unavailable')
  finish()
  assert.equal((await first).status, 'applied')
})

test('undo is not a factory reset and leaves unmodified values alone', () => {
  const before: UIPreferences = { mode: 'system', theme: 'default', currency: 'auto', language: 'en' }
  assert.deepEqual(uiPreferenceUndoPatch(before, { mode: 'dark' }, { ...before, mode: 'light', theme: 'rose-garden' }), {})
})
