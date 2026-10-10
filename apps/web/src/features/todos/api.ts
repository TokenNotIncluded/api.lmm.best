/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { api } from '@/lib/api'

export type TodoCategory =
  | 'all'
  | 'open_source_bounty_review'
  | 'open_source_bounty'
  | 'developer_access'
  | 'account_action'
  | 'security_incident'
  | 'moderation'
  | 'human_support'

export interface TodoItem {
  id: string
  source_id: number
  category: Exclude<TodoCategory, 'all'>
  type: string
  title: string
  summary: string
  read: boolean
  created_at: number
  updated_at: number
  details?: Record<string, unknown>
}

export interface TodoCategorySummary {
  key: Exclude<TodoCategory, 'all'>
  total: number
  unread: number
}

export interface TodoPage {
  items: TodoItem[]
  page: number
  page_size: number
  total: number
  category: TodoCategory
  unread_count: number
  total_unread_count: number
  unread_by_category: Record<string, number>
  categories: TodoCategorySummary[]
}

interface ApiEnvelope<T> {
  success: boolean
  message?: string
  data: T
}

async function unwrap<T>(request: Promise<{ data: ApiEnvelope<T> }>) {
  const response = await request
  if (!response.data.success) {
    throw new Error(response.data.message || 'Todo request failed')
  }
  return response.data.data
}

export function getTodos(
  category: TodoCategory,
  page = 1,
  signal?: AbortSignal
) {
  if (!Number.isSafeInteger(page) || page < 1) {
    throw new RangeError('Todo page must be a positive integer')
  }
  return unwrap<TodoPage>(
    api.get(`/api/todos?category=${category}&p=${page}&page_size=50`, {
      signal,
    })
  ).then((page) => {
    const retired = page.categories.find(
      (item) => item.key === 'developer_access'
    )
    const { developer_access: retiredUnread = 0, ...unread } =
      page.unread_by_category
    return {
      ...page,
      items: page.items.filter((item) => item.category !== 'developer_access'),
      categories: page.categories.filter(
        (item) => item.key !== 'developer_access'
      ),
      unread_by_category: unread,
      // Preserve the old backend's page positions during rolling upgrades.
      // The new backend excludes retired rows before pagination.
      total: page.total,
      unread_count: Math.max(
        0,
        page.unread_count -
          (category === 'all' ? (retired?.unread ?? retiredUnread) : 0)
      ),
      total_unread_count: Math.max(
        0,
        page.total_unread_count - (retired?.unread ?? retiredUnread)
      ),
    }
  })
}

export function markTodoRead(item: TodoItem) {
  return unwrap<{ marked: number }>(
    api.post('/api/todos/read', {
      category: item.category,
      ids: [item.source_id],
      all: false,
    })
  )
}

export function markAllTodosRead() {
  return unwrap<{ marked: number }>(
    api.post('/api/todos/read', { category: 'all', ids: [], all: true })
  )
}
