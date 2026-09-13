/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the License,
or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  normalizePiMessageEnvelopes,
  normalizePiSessionEnvelopes,
  normalizePiSessionMetadata,
  normalizeRemoteControlMessage,
} from './protocol'

test('normalizes the encrypted Pi session list returned by the relay', () => {
  const sessions = normalizePiSessionEnvelopes([
    {
      session_id: 'session_123456',
      device_id: 'device_123456',
      metadata: { nonce: 'nonce-value', ciphertext: 'cipher-value' },
      expires_at: 1_800_000_120,
      updated_at: 1_800_000_000,
    },
    { session_id: 'missing-encrypted-envelope' },
  ])

  assert.equal(sessions.length, 1)
  assert.equal(sessions[0]?.sessionId, 'session_123456')
  assert.equal(sessions[0]?.metadata.ciphertext, 'cipher-value')
})

test('normalizes decrypted metadata and typed messages', () => {
  const envelope = normalizePiSessionEnvelopes([
    {
      session_id: 'session_123456',
      device_id: 'device_123456',
      metadata: { nonce: 'nonce-value', ciphertext: 'cipher-value' },
      expires_at: 1_900_000_120,
      updated_at: 1_900_000_000,
    },
  ])[0]
  assert.ok(envelope)
  const session = normalizePiSessionMetadata(
    {
      version: 1,
      start_time: 1_800_000_000,
      cwd: '/work/project',
      runtime: 'pi 0.85',
      summary: 'Fix tests',
    },
    envelope
  )
  const question = normalizeRemoteControlMessage({
    type: 'ask_user',
    question: 'Continue?',
    options: ['Yes', 'No'],
  })

  assert.equal(session.id, 'session_123456')
  assert.equal(session.deviceId, 'device_123456')
  assert.equal(session.directory, '/work/project')
  assert.equal(question.type, 'ask_user')
  assert.deepEqual(question.options, ['Yes', 'No'])
})

test('normalizes only valid encrypted message envelopes', () => {
  const messages = normalizePiMessageEnvelopes({
    messages: [
      {
        sequence: 1,
        sender: 'plugin',
        nonce: 'nonce-value',
        ciphertext: 'cipher-value',
        created_at: 1_800_000_000,
      },
      {
        sequence: 2,
        sender: 'unknown',
        nonce: 'nonce-value',
        ciphertext: 'cipher-value',
        created_at: 1_800_000_001,
      },
    ],
  })

  assert.equal(messages.length, 1)
  assert.equal(messages[0]?.sender, 'plugin')
  assert.equal(messages[0]?.sequence, 1)
})

test('rejects unsupported metadata versions', () => {
  const envelope = normalizePiSessionEnvelopes([
    {
      session_id: 'session_123456',
      device_id: 'device_123456',
      metadata: { nonce: 'nonce-value', ciphertext: 'cipher-value' },
      expires_at: 1_900_000_120,
      updated_at: 1_900_000_000,
    },
  ])[0]
  assert.ok(envelope)

  assert.throws(() => normalizePiSessionMetadata({ version: 2 }, envelope))
})
