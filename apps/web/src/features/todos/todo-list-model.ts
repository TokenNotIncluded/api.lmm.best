/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import type { TodoCategory, TodoCategorySummary } from './api'

export const TODO_CATEGORY_LABELS: Record<TodoCategory, string> = {
  all: 'All',
  open_source_bounty_review: 'Challenge reviews',
  open_source_bounty: 'Bounty notifications',
  developer_access: 'Developer access',
  account_action: 'Account actions',
  security_incident: 'Security incidents',
  security_review: 'Security reviews',
  human_support: 'Human technical support',
}

const ADMIN_CATEGORIES = new Set<TodoCategory>([
  'developer_access',
  'account_action',
  'security_incident',
  'security_review',
  'human_support',
])

export function visibleTodoCategories(
  summaries: readonly TodoCategorySummary[],
  selected: TodoCategory,
  isAdmin: boolean
): TodoCategory[] {
  const visible = new Set<TodoCategory>(['all'])
  for (const summary of summaries) {
    if (!Object.hasOwn(TODO_CATEGORY_LABELS, summary.key)) continue
    if (
      summary.total > 0 ||
      summary.key === 'open_source_bounty_review' ||
      (isAdmin && ADMIN_CATEGORIES.has(summary.key))
    ) {
      visible.add(summary.key)
    }
  }
  // Keep the active filter reachable when its last item is processed.
  visible.add(selected)
  return [...visible]
}

export function todoPageCount(total: number, pageSize: number) {
  if (!Number.isFinite(total) || total <= 0) return 1
  const size = Number.isSafeInteger(pageSize) && pageSize > 0 ? pageSize : 50
  return Math.max(1, Math.ceil(total / size))
}

export function todoTimestamp(seconds: number) {
  if (!Number.isFinite(seconds) || seconds <= 0) return undefined
  const date = new Date(seconds * 1000)
  return Number.isNaN(date.getTime()) ? undefined : date.toISOString()
}
