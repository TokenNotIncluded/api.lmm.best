/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { api } from '@/lib/api'

import type { AssistantConversationHistoryMessage } from './api'

export type AssistantSupportRequest = {
  id: number
  user_id: number
  conversation_id: number
  kind: 'handoff' | 'appointment'
  status: 'pending' | 'accepted' | 'completed' | 'cancelled'
  topic: string
  preferred_time: string
  scheduled_at: number
  assigned_admin_id: number
  assigned_admin_name: string
  created_at: number
  updated_at: number
  accepted_at: number
  closed_at: number
}
export type AssistantSupportMessage = AssistantConversationHistoryMessage
export type AssistantSupportDetail = {
  request: AssistantSupportRequest
  messages: AssistantSupportMessage[]
}
export type AssistantSupportInput = {
  conversation_id?: number
  kind: 'handoff' | 'appointment'
  topic?: string
  preferred_time?: string
  scheduled_at?: number
}
type Envelope<T> = { success: boolean; message?: string; data: T }
const options = {
  disableDuplicate: true,
  skipBusinessError: true,
  skipErrorHandler: true,
}
function unwrap<T>(response: { data: Envelope<T> }): T {
  if (!response.data.success || response.data.data == null) {
    throw new Error(response.data.message || 'Unable to update human support')
  }
  return response.data.data
}
export function isAssistantSupportActive(
  request?: AssistantSupportRequest | null
) {
  return request?.status === 'pending' || request?.status === 'accepted'
}
export function isAssistantSupportAIPaused(
  request?: AssistantSupportRequest | null
) {
  return (
    request?.status === 'accepted' ||
    (request?.status === 'pending' && request.kind === 'handoff')
  )
}
export async function getAssistantSupportEligibility() {
  return unwrap(
    await api.get<Envelope<{ eligible: boolean }>>(
      '/api/assistant/support/eligibility',
      options
    )
  )
}
export async function getSelfAssistantSupport(conversationId = 0) {
  return unwrap(
    await api.get<Envelope<{ request: AssistantSupportRequest | null }>>(
      '/api/assistant/support/self',
      { ...options, params: { conversation_id: conversationId } }
    )
  )
}
export async function createAssistantSupport(input: AssistantSupportInput) {
  return unwrap(
    await api.post<
      Envelope<{ request: AssistantSupportRequest; created: boolean }>
    >('/api/assistant/support', input, options)
  )
}
export async function getAssistantSupport(id: number) {
  return unwrap(
    await api.get<Envelope<AssistantSupportDetail>>(
      `/api/assistant/support/${id}`,
      options
    )
  )
}
export async function sendAssistantSupportMessage(id: number, content: string) {
  return unwrap(
    await api.post<Envelope<{ message: AssistantSupportMessage }>>(
      `/api/assistant/support/${id}/messages`,
      { content },
      options
    )
  )
}
export async function acceptAssistantSupport(id: number) {
  return unwrap(
    await api.post<Envelope<{ request: AssistantSupportRequest }>>(
      `/api/assistant/support/${id}/accept`,
      {},
      options
    )
  )
}
export async function closeAssistantSupport(id: number, cancel = false) {
  return unwrap(
    await api.post<Envelope<{ request: AssistantSupportRequest }>>(
      `/api/assistant/support/${id}/close`,
      { cancel },
      options
    )
  )
}

// Keep command boundaries aligned with assistantExplicitHumanTransferRequest.
// Quoted examples are not requests to start a support conversation.
export function isExplicitAssistantHandoff(message: string): boolean {
  const text = message
    .replaceAll(
      /"[^"]*"|'[^']*'|`[^`]*`|“[^”]*”|‘[^’]*’|「[^」]*」|『[^』]*』/gs,
      ''
    )
    .trim()
  return (
    /(?:^|[，,。！!\n])\s*(?:请|請|麻烦|麻煩|现在|現在|马上|馬上|直接|帮我|幫我|我要|我想|我需要|给我|給我|请你|請你|一下|\s)*(?:转人工|轉人工|转接人工|轉接人工|人工客服|人工技术支持|人工技術支持|找人工客服|找人工技术支持|找人工技術支持|联系人工客服|聯繫人工客服|联系人工技术支持|聯繫人工技術支持)(?:吧|一下|客服|技术支持|技術支持|服务|服務|\s)*(?:$|[，,。！!])/u.test(
      text
    ) ||
    /^(?:please\s+)?(?:(?:transfer|connect|switch)(?:\s+me)?\s+to\s+(?:a\s+)?(?:human(?:\s+(?:agent|support))?|administrator)|(?:i\s+(?:want|need)\s+to\s+)?(?:speak|talk)\s+to\s+(?:a\s+)?human(?:\s+(?:agent|support))?)[.!]*$/i.test(
      text
    )
  )
}
