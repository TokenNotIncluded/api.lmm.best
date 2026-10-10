/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createRemoteCommand, remoteConversation, latestRemoteState } from './commands'
import { derivePiRemoteKey, encryptPiRemoteCommand } from './crypto'
import { normalizeRemoteControlMessages } from './protocol'

test('controller encrypts commands using the plugin-compatible sender-bound contract', async () => {
  const sessionId = 'remote_test_123'
  const key = await derivePiRemoteKey('fixture-pin', sessionId)
  const command = createRemoteCommand({ action: 'prompt', content: 'private task' })
  const envelope = await encryptPiRemoteCommand(sessionId, command, key)
  assert.ok(!JSON.stringify(envelope).includes('private task'))
  const plain = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: Buffer.from(envelope.nonce, 'base64url'), additionalData: new TextEncoder().encode(`lmm-pi-remote:v1:message:${sessionId}:controller`), tagLength: 128 }, key, Buffer.from(envelope.ciphertext, 'base64url'))
  assert.deepEqual(JSON.parse(new TextDecoder().decode(plain)), command)
  await assert.rejects(crypto.subtle.decrypt({ name: 'AES-GCM', iv: Buffer.from(envelope.nonce, 'base64url'), additionalData: new TextEncoder().encode(`lmm-pi-remote:v1:message:${sessionId}:plugin`), tagLength: 128 }, key, Buffer.from(envelope.ciphertext, 'base64url')))
})
test('unsafe terminal bytes, missing input and oversized tasks are rejected before sending', () => {
  assert.throws(() => createRemoteCommand({ action: 'prompt', content: ' ' }))
  assert.throws(() => createRemoteCommand({ action: 'prompt', content: '界'.repeat(12000) }))
  assert.throws(() => createRemoteCommand({ action: 'ui_input', request_id: 'request_123', text: '\x1b[201~' }))
  assert.throws(() => createRemoteCommand({ action: 'ui_input', request_id: 'request_123' }))
  assert.ok(createRemoteCommand({ action: 'ui_response', request_id: 'request_123', value: 0 }))
})
test('state replaces active questions and stream snapshots do not duplicate the conversation', () => {
  const messages = normalizeRemoteControlMessages([
    { type: 'assistant', id: 'a', content: 'start' },
    { type: 'state', id: 'remote-state', busy: true, requests: [{ request_id: 'request_123', kind: 'select', question: 'Which?', options: ['One', 'Two'] }] },
    { type: 'assistant', id: 'a', content: 'complete' },
    { type: 'ack', command_id: 'command_123', ok: true },
    { type: 'state', id: 'remote-state', busy: false, requests: [] },
  ])
  assert.deepEqual(remoteConversation(messages).map((message) => message.content), ['complete'])
  assert.deepEqual(latestRemoteState(messages)?.requests, [])
  assert.equal(messages[1]?.requests?.[0]?.kind, 'select')
  assert.equal(messages[3]?.command_id, 'command_123')
})
