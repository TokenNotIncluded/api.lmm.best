/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import type {
  PiRemoteMessageEnvelope,
  PiRemoteSession,
  PiRemoteSessionEnvelope,
  RemoteControlMessage,
  RemoteControlMessageType,
  RemoteQuestion,
} from './types'

const messageTypes = new Set<RemoteControlMessageType>([
  'user',
  'assistant',
  'system',
  'thinking',
  'tool_call',
  'tool_result',
  'ask_user',
  'state',
  'ack',
])

function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null
}

function text(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value : undefined
}

function finiteNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function ciphertext(value: unknown) {
  const source = record(value)
  const nonce = text(source?.nonce)
  const encrypted = text(source?.ciphertext)
  return nonce && encrypted ? { nonce, ciphertext: encrypted } : null
}

export function normalizeRemoteControlMessages(
  value: unknown
): RemoteControlMessage[] {
  if (!Array.isArray(value)) return []
  return value.flatMap((item): RemoteControlMessage[] => {
    const source = record(item)
    if (!source) return []
    const rawType = text(source.type) ?? 'assistant'
    const type = messageTypes.has(rawType as RemoteControlMessageType)
      ? (rawType as RemoteControlMessageType)
      : 'assistant'
    return [
      {
        id: text(source.id),
        type,
        content: text(source.content),
        title: text(source.title),
        tool_name: text(source.tool_name) ?? text(source.toolName),
        arguments: source.arguments,
        command_id: text(source.command_id),
        ok: source.ok === true,
        busy: source.busy === true,
        provider: text(source.provider),
        model: text(source.model),
        requests: source.type === 'state' ? normalizeRemoteQuestions(source.requests) : undefined,
        question: text(source.question),
        options: Array.isArray(source.options)
          ? source.options.filter(
              (option): option is string => typeof option === 'string'
            )
          : undefined,
        created_at:
          typeof source.created_at === 'string' ||
          typeof source.created_at === 'number'
            ? source.created_at
            : undefined,
      },
    ]
  })
}

export function normalizePiSessionEnvelopes(
  payload: unknown
): PiRemoteSessionEnvelope[] {
  const envelope = record(payload)
  const raw = Array.isArray(payload)
    ? payload
    : Array.isArray(envelope?.sessions)
      ? envelope.sessions
      : []

  return raw.flatMap((item): PiRemoteSessionEnvelope[] => {
    const source = record(item)
    if (!source) return []
    const sessionId = text(source.session_id)
    const deviceId = text(source.device_id)
    const metadata = ciphertext(source.metadata)
    const expiresAt = finiteNumber(source.expires_at)
    const updatedAt = finiteNumber(source.updated_at)
    if (!sessionId || !deviceId || !metadata || !expiresAt || !updatedAt) {
      return []
    }

    return [
      {
        sessionId,
        deviceId,
        metadata,
        expiresAt,
        updatedAt,
      },
    ]
  })
}

export function normalizePiMessageEnvelopes(
  payload: unknown
): PiRemoteMessageEnvelope[] {
  const envelope = record(payload)
  const raw = Array.isArray(payload)
    ? payload
    : Array.isArray(envelope?.messages)
      ? envelope.messages
      : []

  return raw.flatMap((item): PiRemoteMessageEnvelope[] => {
    const source = record(item)
    if (!source) return []
    const sequence = finiteNumber(source.sequence)
    const sender = source.sender
    const encrypted = ciphertext(source)
    const createdAt = finiteNumber(source.created_at)
    if (
      !Number.isSafeInteger(sequence) ||
      sequence === undefined ||
      sequence < 1 ||
      (sender !== 'plugin' && sender !== 'controller') ||
      !encrypted ||
      createdAt === undefined
    ) {
      return []
    }
    return [{ sequence, sender, createdAt, ...encrypted }]
  })
}

export function normalizePiSessionMetadata(
  payload: unknown,
  envelope: PiRemoteSessionEnvelope
): PiRemoteSession {
  const source = record(payload)
  if (!source || (source.version !== undefined && source.version !== 1)) {
    throw new Error('Invalid Pi session metadata')
  }

  const startedAt =
    (typeof source.started_at === 'string' ||
    typeof source.started_at === 'number'
      ? source.started_at
      : undefined) ??
    (typeof source.start_time === 'string' ||
    typeof source.start_time === 'number'
      ? source.start_time
      : undefined)

  return {
    id: envelope.sessionId,
    deviceId: envelope.deviceId,
    active: envelope.expiresAt * 1000 > Date.now(),
    startedAt,
    runtime: text(source.runtime),
    capabilities: Array.isArray(source.capabilities) ? source.capabilities.filter((value): value is string => typeof value === 'string' && ['prompt', 'abort', 'ui_response', 'ui_input'].includes(value)) : [],
    directory: text(source.directory) ?? text(source.cwd),
    summary: text(source.summary),
    messages: normalizeRemoteControlMessages(source.messages),
  }
}

export function normalizeRemoteControlMessage(
  payload: unknown
): RemoteControlMessage {
  const normalized = normalizeRemoteControlMessages([payload])
  if (!normalized[0]) throw new Error('Invalid Pi remote-control message')
  return normalized[0]
}

export function isCollapsedMessage(type: RemoteControlMessageType): boolean {
  return type === 'thinking' || type === 'tool_call' || type === 'tool_result'
}

export function normalizeRemoteQuestions(value: unknown): RemoteQuestion[] {
  if (!Array.isArray(value)) return []
  return value.slice(0, 4).flatMap((item): RemoteQuestion[] => {
    const source = record(item)
    if (!source || typeof source.request_id !== 'string' || !/^[A-Za-z0-9_-]{8,64}$/.test(source.request_id)) return []
    const kind = source.kind
    if (kind !== 'select' && kind !== 'confirm' && kind !== 'input' && kind !== 'custom') return []
    return [{ request_id: source.request_id, kind, question: text(source.question)?.slice(0, 4000) ?? '',
      title: text(source.title)?.slice(0, 1000), content: text(source.content)?.slice(0, 10000),
      placeholder: text(source.placeholder)?.slice(0, 1000), expires_at: finiteNumber(source.expires_at),
      options: Array.isArray(source.options) ? source.options.slice(0, 100).filter((option): option is string => typeof option === 'string').map((option) => option.slice(0, 512)) : undefined }]
  })
}
