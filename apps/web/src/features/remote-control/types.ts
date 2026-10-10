/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
export type RemoteControlMessageType =
  | 'user'
  | 'assistant'
  | 'system'
  | 'thinking'
  | 'tool_call'
  | 'tool_result'
  | 'ask_user'
  | 'state'
  | 'ack'

export type RemoteControlMessage = {
  id?: string
  type: RemoteControlMessageType
  content?: string
  title?: string
  tool_name?: string
  arguments?: unknown
  question?: string
  options?: string[]
  command_id?: string
  ok?: boolean
  busy?: boolean
  provider?: string
  model?: string
  requests?: RemoteQuestion[]
  created_at?: string | number
}

export type PiRemoteCiphertext = {
  nonce: string
  ciphertext: string
}

export type PiRemoteSessionEnvelope = {
  sessionId: string
  deviceId: string
  metadata: PiRemoteCiphertext
  expiresAt: number
  updatedAt: number
}

export type PiRemoteMessageEnvelope = PiRemoteCiphertext & {
  sequence: number
  sender: 'plugin' | 'controller'
  createdAt: number
}

export type PiRemoteSession = {
  id: string
  deviceId: string
  active: boolean
  capabilities?: string[]
  startedAt?: string | number
  runtime?: string
  directory?: string
  summary?: string
  messages: RemoteControlMessage[]
}

export type PiSessionsResponse = {
  success: boolean
  message?: string
  data?: unknown
}

export type PiMessagesResponse = PiSessionsResponse

export type RemoteQuestion = {
  request_id: string
  kind: 'select' | 'confirm' | 'input' | 'custom'
  title?: string
  question: string
  content?: string
  placeholder?: string
  options?: string[]
  expires_at?: number
}
