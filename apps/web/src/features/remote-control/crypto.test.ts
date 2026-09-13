/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  decryptPiRemoteMessage,
  decryptPiSessionMetadata,
  derivePiRemoteKey,
  PI_REMOTE_KDF_ITERATIONS,
} from './crypto'
import type { PiRemoteMessageEnvelope, PiRemoteSessionEnvelope } from './types'

const encoder = new TextEncoder()

function base64Url(value: ArrayBuffer) {
  return Buffer.from(value).toString('base64url')
}

async function encryptionKey(pin: string, sessionId: string) {
  const material = await crypto.subtle.importKey(
    'raw',
    encoder.encode(pin),
    'PBKDF2',
    false,
    ['deriveKey']
  )
  return crypto.subtle.deriveKey(
    {
      name: 'PBKDF2',
      hash: 'SHA-256',
      iterations: PI_REMOTE_KDF_ITERATIONS,
      salt: encoder.encode(`lmm-pi-remote:v1:${sessionId}`),
    },
    material,
    { name: 'AES-GCM', length: 256 },
    false,
    ['encrypt']
  )
}

async function encrypt(
  key: CryptoKey,
  value: unknown,
  additionalData: string,
  nonceByte: number
) {
  const nonce = new Uint8Array(12).fill(nonceByte)
  const ciphertext = await crypto.subtle.encrypt(
    {
      name: 'AES-GCM',
      iv: nonce,
      additionalData: encoder.encode(additionalData),
      tagLength: 128,
    },
    key,
    encoder.encode(JSON.stringify(value))
  )
  return { nonce: base64Url(nonce.buffer), ciphertext: base64Url(ciphertext) }
}

test('decrypts v1 metadata and message envelopes with the shared PIN', async () => {
  const pin = '42-forest'
  const sessionId = 'session_123456'
  const deviceId = 'device_123456'
  const encryptKey = await encryptionKey(pin, sessionId)
  const metadata = await encrypt(
    encryptKey,
    {
      version: 1,
      runtime: 'pi 0.85.1',
      directory: '/work/project',
      summary: 'Fix the release',
    },
    `lmm-pi-remote:v1:metadata:${sessionId}:${deviceId}`,
    7
  )
  const messageCiphertext = await encrypt(
    encryptKey,
    { version: 1, type: 'assistant', content: 'Done' },
    `lmm-pi-remote:v1:message:${sessionId}:plugin`,
    8
  )
  const envelope: PiRemoteSessionEnvelope = {
    sessionId,
    deviceId,
    metadata,
    updatedAt: 1_800_000_000,
    expiresAt: 1_900_000_000,
  }
  const messageEnvelope: PiRemoteMessageEnvelope = {
    sequence: 1,
    sender: 'plugin',
    createdAt: 1_800_000_001,
    ...messageCiphertext,
  }
  const decryptKey = await derivePiRemoteKey(pin, sessionId)

  const session = await decryptPiSessionMetadata(envelope, decryptKey)
  const message = await decryptPiRemoteMessage(
    sessionId,
    messageEnvelope,
    decryptKey
  )

  assert.equal(session.runtime, 'pi 0.85.1')
  assert.equal(session.summary, 'Fix the release')
  assert.equal(message.content, 'Done')
  assert.equal(message.id, 'session_123456-1')
})

test('rejects the wrong PIN and tampered routing metadata', async () => {
  const sessionId = 'session_123456'
  const deviceId = 'device_123456'
  const encryptKey = await encryptionKey('correct', sessionId)
  const metadata = await encrypt(
    encryptKey,
    { version: 1, runtime: 'pi 0.85.1' },
    `lmm-pi-remote:v1:metadata:${sessionId}:${deviceId}`,
    9
  )
  const envelope: PiRemoteSessionEnvelope = {
    sessionId,
    deviceId,
    metadata,
    updatedAt: 1_800_000_000,
    expiresAt: 1_900_000_000,
  }

  await assert.rejects(
    decryptPiSessionMetadata(
      envelope,
      await derivePiRemoteKey('wrong', sessionId)
    )
  )
  await assert.rejects(
    decryptPiSessionMetadata(
      { ...envelope, deviceId: 'device_tampered' },
      await derivePiRemoteKey('correct', sessionId)
    )
  )
})
