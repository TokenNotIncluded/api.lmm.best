/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { api } from '@/lib/api'

import { StoreAPIError } from './api'

export type SupportRole = 'buyer' | 'seller'
export type SupportStatus = 'open' | 'resolved'
export interface SupportConversation {
  id: string
  buyer_id: number
  seller_id: number
  product_id: string
  order_id: string
  subject: string
  status: SupportStatus
  last_message_id: number
  buyer_read_id: number
  seller_read_id: number
  created_at: number
  updated_at: number
}
export interface SupportThread extends SupportConversation {
  buyer_name: string
  seller_name: string
  unread_count: number
}
export interface SupportMessage {
  id: number
  conversation_id: string
  sender_id: number
  body: string
  created_at: number
}
export interface SupportHistory {
  conversation: SupportConversation
  items: SupportMessage[]
  has_more: boolean
}
export interface StoreCustomer {
  buyer_id: number
  display_name: string
  order_count: number
  paid_quota: number
  last_order_at: number
  last_order_id: string
  conversation_id: string
  note: string
  tags: string[]
  revision: number
}
export interface SupportAssistantContext {
  role: SupportRole
  conversation_id: string
  subject: string
  order?: { id: string; status: string; quantity: number }
  messages: { role: SupportRole; text: string }[]
}
export interface SupportPage<T> {
  items: T[]
  has_more: boolean
}

const root = '/api/store'
const options = { skipErrorHandler: true, skipBusinessError: true }
const conversationPath = (id: string) =>
  `${root}/support/conversations/${encodeURIComponent(id)}`

async function read<T>(
  request: Promise<{
    data: { success: boolean; data: T; code?: string; message?: string }
  }>
): Promise<T> {
  try {
    const { data } = await request
    if (!data.success) throw new StoreAPIError(data)
    return data.data
  } catch (error) {
    if (error instanceof StoreAPIError) throw error
    throw new StoreAPIError(
      (error as { response?: { data?: { code?: string; message?: string } } })
        ?.response?.data
    )
  }
}

export const supportApi = {
  conversations: (
    role: SupportRole,
    status: string,
    unread: boolean,
    page: number,
    signal?: AbortSignal
  ) =>
    read<SupportPage<SupportThread>>(
      api.get(`${root}/support/conversations`, {
        ...options,
        signal,
        params: { role, status, unread, offset: (page - 1) * 30, limit: 30 },
      })
    ),
  open: (
    input: { product_id?: string; order_id?: string },
    signal?: AbortSignal
  ) =>
    read<SupportConversation>(
      api.post(`${root}/support/conversations`, input, { ...options, signal })
    ),
  history: (id: string, before = 0, signal?: AbortSignal) =>
    read<SupportHistory>(
      api.get(`${conversationPath(id)}/messages`, {
        ...options,
        signal,
        params: { before, limit: 50 },
      })
    ),
  send: (id: string, body: string, requestKey: string) =>
    read<SupportMessage>(
      api.post(
        `${conversationPath(id)}/messages`,
        { body, request_key: requestKey },
        options
      )
    ),
  markRead: (id: string, through: number) =>
    read<Record<string, never>>(
      api.put(`${conversationPath(id)}/read`, { through_id: through }, options)
    ),
  status: (id: string, status: SupportStatus) =>
    read<Record<string, never>>(
      api.put(`${conversationPath(id)}/status`, { status }, options)
    ),
  customers: (search: string, page: number, signal?: AbortSignal) =>
    read<SupportPage<StoreCustomer>>(
      api.get(`${root}/my/customers`, {
        ...options,
        signal,
        params: { q: search, offset: (page - 1) * 30, limit: 30 },
      })
    ),
  saveCustomer: (
    buyerId: number,
    input: Pick<StoreCustomer, 'note' | 'tags' | 'revision'>
  ) =>
    read<Record<string, never>>(
      api.put(`${root}/my/customers/${buyerId}`, input, options)
    ),
  assistantContext: (id: string, signal?: AbortSignal) =>
    read<SupportAssistantContext>(
      api.get(`${conversationPath(id)}/assistant-context`, {
        ...options,
        signal,
      })
    ),
}
