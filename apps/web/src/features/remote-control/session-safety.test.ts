/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import test from 'node:test'

import {
  normalizePiMessageEnvelopes,
  normalizePiSessionEnvelopes,
} from './protocol'
import { isRemoteStateFresh, REMOTE_STATE_TTL_MS } from './session-safety'

test('heartbeat freshness expires even with identical successful polling snapshots', () => {
  const now = 1_900_000_000_000
  assert.equal(isRemoteStateFresh(now, now), true)
  assert.equal(isRemoteStateFresh(now, now + REMOTE_STATE_TTL_MS - 1), true)
  assert.equal(isRemoteStateFresh(now, now + REMOTE_STATE_TTL_MS), false)
  for (const timestamp of [
    undefined,
    0,
    '1900000000000',
    Number.NaN,
    Infinity,
    now + 5001,
    now + 0.5,
  ]) {
    assert.equal(isRemoteStateFresh(timestamp, now), false)
  }
})
test('relay responses enforce bounded lists and preserve restart identity', () => {
  assert.throws(() => normalizePiSessionEnvelopes(Array(33).fill({})))
  assert.throws(() =>
    normalizePiMessageEnvelopes({ messages: Array(129).fill({}) })
  )
  const raw = {
    session_id: 'session_fixture',
    device_id: 'device_fixture',
    generation: 'generation_fixture',
    metadata: { nonce: 'fixture', ciphertext: 'fixture' },
    expires_at: 1_900_000_120,
    updated_at: 1_900_000_000,
  }
  assert.equal(
    normalizePiSessionEnvelopes([raw])[0]?.generation,
    'generation_fixture'
  )
  assert.equal(
    normalizePiSessionEnvelopes([{ ...raw, session_id: '../foreign' }]).length,
    0
  )
  assert.equal(
    normalizePiSessionEnvelopes([{ ...raw, expires_at: Infinity }]).length,
    0
  )
  assert.equal(
    normalizePiSessionEnvelopes([{ ...raw, updated_at: -1 }]).length,
    0
  )
})
