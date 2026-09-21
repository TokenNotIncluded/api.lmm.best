/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.
*/
import {
  normalizePiSessionMetadata,
  normalizeRemoteControlMessage,
} from './protocol'
import type {
  PiRemoteCiphertext,
  PiRemoteMessageEnvelope,
  PiRemoteSessionEnvelope,
  RemoteControlMessage,
} from './types'

export const PI_REMOTE_KDF_ITERATIONS = 210_000

const encoder = new TextEncoder()
const decoder = new TextDecoder('utf-8', { fatal: true })

function decodeBase64Url(value: string): Uint8Array<ArrayBuffer> {
  if (!value || value.includes('=') || !/^[A-Za-z0-9_-]+$/.test(value)) {
    throw new Error('Invalid base64url payload')
  }
  const base64 = value.replaceAll('-', '+').replaceAll('_', '/')
  const padded = base64.padEnd(Math.ceil(base64.length / 4) * 4, '=')
  const binary = atob(padded)
  const bytes = new Uint8Array(binary.length)
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index)
  }
  return bytes
}

function metadataAdditionalData(sessionId: string, deviceId: string) {
  return encoder.encode(`lmm-pi-remote:v1:metadata:${sessionId}:${deviceId}`)
}

function messageAdditionalData(sessionId: string, sender: string) {
  return encoder.encode(`lmm-pi-remote:v1:message:${sessionId}:${sender}`)
}

export async function derivePiRemoteKey(
  pin: string,
  sessionId: string
): Promise<CryptoKey> {
  if (!pin || pin.length > 128) throw new Error('Invalid PIN')
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
    ['decrypt']
  )
}

async function decryptJson(
  key: CryptoKey,
  envelope: PiRemoteCiphertext,
  additionalData: Uint8Array<ArrayBuffer>
): Promise<unknown> {
  const nonce = decodeBase64Url(envelope.nonce)
  if (nonce.byteLength !== 12) throw new Error('Invalid AES-GCM nonce')
  const plaintext = await crypto.subtle.decrypt(
    {
      name: 'AES-GCM',
      iv: nonce,
      additionalData,
      tagLength: 128,
    },
    key,
    decodeBase64Url(envelope.ciphertext)
  )
  return JSON.parse(decoder.decode(plaintext))
}

export async function decryptPiSessionMetadata(
  envelope: PiRemoteSessionEnvelope,
  key: CryptoKey
) {
  const payload = await decryptJson(
    key,
    envelope.metadata,
    metadataAdditionalData(envelope.sessionId, envelope.deviceId)
  )
  return normalizePiSessionMetadata(payload, envelope)
}

export async function decryptPiRemoteMessage(
  sessionId: string,
  envelope: PiRemoteMessageEnvelope,
  key: CryptoKey
): Promise<RemoteControlMessage> {
  const payload = await decryptJson(
    key,
    envelope,
    messageAdditionalData(sessionId, envelope.sender)
  )
  const message = normalizeRemoteControlMessage(payload)
  return {
    ...message,
    id: message.id ?? `${sessionId}-${envelope.sequence}`,
    created_at: message.created_at ?? envelope.createdAt,
  }
}
