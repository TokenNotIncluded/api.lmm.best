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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { todoItemTitleKey } from './todo-labels'
import {
  todoItemHasDestination,
  todoSecurityReviewDestination,
} from './todo-navigation'

function item(
  category:
    | 'open_source_bounty'
    | 'security_review'
    | 'developer_access'
    | 'account_action'
    | 'human_support',
  details: Record<string, unknown> = {}
) {
  return {
    id: `${category}:1`,
    source_id: 1,
    category,
    type: 'test',
    title: 'test',
    summary: 'test',
    read: false,
    created_at: 1,
    updated_at: 1,
    details,
  } as const
}

describe('unified todo destinations', () => {
  test('uses the locale key for assistant security reviews', () => {
    assert.equal(
      todoItemTitleKey('assistant.security_review'),
      'assistant.security_review'
    )
    assert.equal(todoItemTitleKey('unknown.todo'), 'Notification')
  })

  test('advertises security review navigation even without a user or project id', () => {
    const review = item('security_review')
    assert.equal(todoItemHasDestination(review), true)
    assert.equal(todoSecurityReviewDestination(review), '/security')
  })

  test('keeps destination affordances tied to actionable notification data', () => {
    assert.equal(todoItemHasDestination(item('open_source_bounty')), false)
    assert.equal(
      todoItemHasDestination(item('open_source_bounty', { project_id: 12 })),
      true
    )
    // The request ID is enough for an administrator to open the review panel;
    // resolving a user profile first would hide the actionable approve/reject UI.
    assert.equal(todoItemHasDestination(item('developer_access')), true)
    assert.equal(todoItemHasDestination(item('account_action')), true)
    assert.equal(todoItemHasDestination(item('human_support')), true)
    assert.equal(
      todoItemTitleKey('assistant.human_support'),
      'Human technical support'
    )
  })
})
