/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import type { RemoteControlMessage } from './types'

export const remoteKeys = ['up', 'down', 'left', 'right', 'enter', 'escape', 'tab', 'backspace', 'home', 'end', 'pageup', 'pagedown', 'space', 'ctrl+s', 'ctrl+u'] as const
export type RemoteKey = (typeof remoteKeys)[number]
export type RemoteCommandInput =
  | { action: 'prompt'; content: string; delivery?: 'steer' | 'followUp' }
  | { action: 'abort' }
  | { action: 'ui_response'; request_id: string; value?: string | boolean | number; cancelled?: boolean }
  | { action: 'ui_input'; request_id: string; key?: RemoteKey; text?: string }
export type RemoteCommand = RemoteCommandInput & { version: 1; type: 'command'; id: string; issued_at: number }

export function createRemoteCommand(input: RemoteCommandInput): RemoteCommand {
  if (input.action === 'prompt' && (!input.content.trim() || new TextEncoder().encode(input.content).length > 32_000)) throw new Error('Invalid task length')
  if ((input.action === 'ui_input' || input.action === 'ui_response') && !/^[A-Za-z0-9_-]{8,64}$/.test(input.request_id)) throw new Error('Invalid question ID')
  if (input.action === 'ui_input') {
    if (input.key !== undefined && (!remoteKeys.includes(input.key) || input.text !== undefined)) throw new Error('Invalid key')
    if (input.text !== undefined && (input.key !== undefined || !input.text || new TextEncoder().encode(input.text).length > 8_000 || /[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(input.text))) throw new Error('Invalid text input')
    if (input.key === undefined && input.text === undefined) throw new Error('Missing input')
  }
  return { ...input, version: 1, type: 'command', id: crypto.randomUUID(), issued_at: Date.now() }
}

/** Same-ID stream snapshots replace older copies. Control events stay out of chat. */
export function remoteConversation(messages: RemoteControlMessage[]): RemoteControlMessage[] {
  const latest = new Map<string, RemoteControlMessage>()
  for (const [index, message] of messages.entries()) {
    if (message.type === 'state' || message.type === 'ack') continue
    latest.set(message.id ?? `message-${index}`, message)
  }
  return [...latest.values()]
}
export function latestRemoteState(messages: RemoteControlMessage[]): RemoteControlMessage | undefined {
  for (let index = messages.length - 1; index >= 0; index--) {
    if (messages[index].type === 'state') return messages[index]
  }
  return undefined
}
