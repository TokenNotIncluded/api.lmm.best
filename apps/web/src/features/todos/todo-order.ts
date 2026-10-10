/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import type { TodoItem } from './api'

/**
 * The to-do feed is a read-only server projection: `/api/todos` only supports
 * listing and read receipts, and `TodoItem` carries no ordering field. A custom
 * order is therefore a per-browser view preference, never a server write.
 *
 * Identity has to be the composite key because the same `source_id` can appear
 * in several categories.
 */

const STORAGE_PREFIX = 'lmm.todos.order'

export function todoOrderKey(
  item: Pick<TodoItem, 'category' | 'id' | 'source_id'>
) {
  return item.id || `${item.category}:${item.source_id}`
}

export function todoOrderStorageKey(userId: number | undefined) {
  return userId === undefined ? STORAGE_PREFIX : `${STORAGE_PREFIX}.${userId}`
}

function parseOrder(raw: string | null): string[] {
  if (!raw) return []
  try {
    const value: unknown = JSON.parse(raw)
    if (!Array.isArray(value)) return []
    return value.filter((entry): entry is string => typeof entry === 'string')
  } catch {
    return []
  }
}

/** Ordering is a convenience; a blocked or full store must not break the feed. */
export function readTodoOrder(userId: number | undefined): string[] {
  if (typeof window === 'undefined') return []
  try {
    return parseOrder(window.localStorage.getItem(todoOrderStorageKey(userId)))
  } catch {
    return []
  }
}

export function writeTodoOrder(userId: number | undefined, order: string[]) {
  if (typeof window === 'undefined') return
  try {
    const key = todoOrderStorageKey(userId)
    if (order.length === 0) {
      window.localStorage.removeItem(key)
      return
    }
    window.localStorage.setItem(key, JSON.stringify(order))
  } catch {
    // A private window or a full quota silently keeps the server order.
  }
}

/**
 * Applies the saved preference without losing items the server has not
 * returned yet (later pages, or rows created since the order was saved).
 * Unranked items keep their incoming relative position behind ranked ones.
 */
export function applyTodoOrder<
  T extends Pick<TodoItem, 'category' | 'id' | 'source_id'>,
>(items: readonly T[], order: readonly string[]): T[] {
  if (!order.length || items.length < 2) return [...items]
  const rank = new Map(order.map((id, index) => [id, index]))
  return items
    .map((item, index) => ({ item, index, rank: rank.get(todoOrderKey(item)) }))
    .sort((left, right) => {
      if (left.rank === undefined && right.rank === undefined) {
        return left.index - right.index
      }
      if (left.rank === undefined) return 1
      if (right.rank === undefined) return -1
      return left.rank - right.rank
    })
    .map((entry) => entry.item)
}

/**
 * Reorders the visible window and appends any previously ranked key that is no
 * longer on screen, so paging does not discard a saved preference.
 */
export function mergeTodoOrder(
  visible: readonly string[],
  previous: readonly string[]
) {
  const next = new Set(visible)
  return [...visible, ...previous.filter((id) => !next.has(id))]
}
