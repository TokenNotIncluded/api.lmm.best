/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import 'fake-indexeddb/auto'

import {
  createCircuit,
  rotateTile,
  traceCircuit,
} from '@/features/auth/components/signal-game'
import { api } from '@/lib/api'

import { HUMAN } from './api'
import { readLocalRecords } from './storage'
import {
  loadSignalRecords,
  rotateSignalTile,
  signalSnapshot,
  startSignalGame,
  uploadSignalRecord,
  useSignalGame,
} from './store'

const AI = {
  actor: 'ai' as const,
  model_id: 'test-model',
  harness: 'codex',
  agent_name: 'test-agent',
}
function completePractice() {
  const { circuit } = useSignalGame.getState()
  for (const index of circuit.route) {
    while (useSignalGame.getState().tiles[index] !== circuit.solution[index]) {
      if (useSignalGame.getState().phase === 'won') return
      rotateSignalTile(index)
    }
  }
}
test('guest practice survives reload and cannot upload while signed out', async () => {
  await startSignalGame('practice', 5, HUMAN, 42)
  completePractice()
  const id = useSignalGame.getState().currentRecord
  assert.ok(id)
  await new Promise((resolve) => setTimeout(resolve, 30))
  const stored = await readLocalRecords()
  assert.ok(stored.some((row) => row.id === id && row.elapsed_ms === null))
  useSignalGame.setState({ records: [] })
  await loadSignalRecords()
  assert.ok(useSignalGame.getState().records.some((row) => row.id === id))
  await assert.rejects(uploadSignalRecord(id, true), /Sign in/)
})
test('AI identity is required before tool play and practice is never timed', async () => {
  await startSignalGame('practice', 5, HUMAN, 42)
  assert.throws(() => rotateSignalTile(0, 'ai'), /Start an AI round/)
  await assert.rejects(
    startSignalGame('practice', 5, { ...AI, model_id: '' }),
    /required/
  )
  await startSignalGame('practice', 64, AI, 42)
  assert.equal(signalSnapshot().tiles.length, 4096)
  assert.equal(signalSnapshot().elapsed_ms, null)
  rotateSignalTile(0, 'ai')
  assert.equal(signalSnapshot().participant.harness, 'codex')
})
test('challenge countdown blocks moves and challenge hints stay forbidden', async () => {
  const originalPost = api.post,
    originalTimeout = globalThis.setTimeout
  let finishCountdown: (() => void) | undefined
  globalThis.setTimeout = ((
    callback: () => void,
    delay?: number,
    ...rest: unknown[]
  ) => {
    if (delay === 3000) {
      finishCountdown = callback
      return originalTimeout(() => undefined, 100000)
    }
    return originalTimeout(callback, delay, ...rest)
  }) as typeof setTimeout
  api.post = (async (url: string) => {
    assert.equal(url, '/api/games/signal/attempts')
    return {
      data: {
        success: true,
        data: {
          seed: 42,
          size: 5,
          day: '2026-09-19',
          rules_version: 2,
          countdown_seconds: 3,
          token: 'x'.repeat(43),
        },
      },
    }
  }) as typeof api.post
  try {
    await startSignalGame('challenge', 5, AI)
    assert.equal(signalSnapshot().phase, 'countdown')
    assert.throws(() => rotateSignalTile(0, 'ai'), /countdown/)
    assert.ok(finishCountdown)
    finishCountdown()
    assert.equal(signalSnapshot().phase, 'playing')
    assert.throws(() => rotateSignalTile(-1, 'ai'), /forbidden/)
    assert.equal(useSignalGame.getState().actions.length, 0)
  } finally {
    api.post = originalPost
    globalThis.setTimeout = originalTimeout
    await startSignalGame('practice', 5, HUMAN, 42)
  }
})
test('all selectable sizes use bounded, solvable boards', () => {
  for (const size of [5, 8, 12, 24, 48, 64]) {
    for (const seed of [0, 42, 4294967295]) {
      const c = createCircuit(seed, size)
      assert.equal(c.tiles.length, size * size)
      assert.equal(traceCircuit(c.tiles, size).won, false)
      assert.equal(traceCircuit(c.solution, size).won, true)
      for (let i = 0; i < c.tiles.length; i++) {
        let mask = c.tiles[i]
        let found = false
        for (let turn = 0; turn < 4; turn++) {
          if (mask === c.solution[i]) found = true
          mask = rotateTile(mask)
        }
        assert.ok(found)
      }
    }
  }
})
