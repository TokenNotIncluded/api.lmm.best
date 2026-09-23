/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import 'fake-indexeddb/auto'

import { useSignalGame } from './store'
import { signalGameTools } from './webmcp'

test('WebMCP requires AI identity and round ownership, bounds batches, and never exposes the solution', async () => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'window')
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: { location: { pathname: '/games/signal' } },
  })
  const tools = signalGameTools(),
    options = { signal: new AbortController().signal }
  const get = (name: string) => {
    const tool = tools.find((row) => row.name === name)
    assert.ok(tool)
    return tool
  }
  try {
    await assert.rejects(
      get('lmm_signal_start').execute(
        {
          mode: 'practice',
          size: 5,
          harness: 'codex',
          agent_name: 'Test agent',
        },
        options
      ),
      /model_id/
    )
    const started = (await get('lmm_signal_start').execute(
      {
        mode: 'practice',
        size: 5,
        model_id: 'test-model',
        harness: 'codex',
        agent_name: 'Test agent',
      },
      options
    )) as { round_id: string; participant: { actor: string } }
    assert.equal(started.participant.actor, 'ai')
    assert.doesNotMatch(JSON.stringify(started), /"solution"|"token"/)
    await assert.rejects(
      get('lmm_signal_rotate').execute(
        { round_id: 'old', tile_indices: [0] },
        options
      ),
      /Round changed/
    )
    await assert.rejects(
      get('lmm_signal_rotate').execute(
        { round_id: started.round_id, tile_indices: Array(33).fill(0) },
        options
      ),
      /Invalid tile batch/
    )
    assert.equal(useSignalGame.getState().actions.length, 0)
    await get('lmm_signal_rotate').execute(
      { round_id: started.round_id, tile_indices: [0, 0, 0, 0] },
      options
    )
    assert.equal(useSignalGame.getState().actions.length, 4)
    await assert.rejects(
      get('lmm_signal_submit').execute(
        { record_id: 'missing', publish: true },
        options
      ),
      /confirm: true/
    )
    await assert.rejects(
      get('lmm_signal_submit').execute(
        { record_id: 'missing', publish: true, confirm: true },
        options
      ),
      /Sign in/
    )
    assert.equal(
      get('lmm_signal_records').annotations?.untrustedContentHint,
      true
    )
  } finally {
    if (previous) Object.defineProperty(globalThis, 'window', previous)
    else delete (globalThis as { window?: unknown }).window
  }
})
