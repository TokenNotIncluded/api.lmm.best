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
/*
Copyright (C) 2026 LIghtJUNction
*/
import type { TodoItem } from './api'

export function todoDetailString(item: TodoItem, key: string) {
  const value = item.details?.[key]
  return typeof value === 'string' ? value : ''
}

export function todoDetailNumber(item: TodoItem, key: string) {
  const value = item.details?.[key]
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function positiveId(value: number | undefined) {
  return value !== undefined && Number.isSafeInteger(value) && value > 0
}

export function todoItemHasDestination(item: TodoItem) {
  switch (item.category) {
    case 'open_source_bounty':
    case 'open_source_bounty_review':
      return positiveId(todoDetailNumber(item, 'project_id'))
    case 'security_review':
      return true
    case 'human_support':
    case 'developer_access':
    case 'account_action':
      return positiveId(item.source_id)
    case 'security_incident':
      return Boolean(todoDetailString(item, 'username').trim())
    default:
      return false
  }
}

export function todoItemCanOpen(item: TodoItem, isAdmin: boolean) {
  const publicDestination =
    item.category === 'open_source_bounty' ||
    item.category === 'open_source_bounty_review'
  return (isAdmin || publicDestination) && todoItemHasDestination(item)
}

/** Security reviews belong to the audit timeline, not the settings form. */
export function todoSecurityReviewDestination(item: TodoItem) {
  return item.category === 'security_review'
    ? ('/security' as const)
    : undefined
}
