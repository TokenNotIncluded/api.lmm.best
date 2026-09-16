/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import type { TodoCategorySummary, TodoItem } from './api'
import {
  todoPageCount,
  todoTimestamp,
  visibleTodoCategories,
} from './todo-list-model'
import {
  todoDetailNumber,
  todoItemCanOpen,
  todoItemHasDestination,
  todoSecurityReviewDestination,
} from './todo-navigation'

function item(
  category: TodoItem['category'],
  details: Record<string, unknown> = {}
): TodoItem {
  return {
    id: `${category}:1`,
    source_id: 1,
    category,
    type: 'test',
    title: 'test',
    summary: '',
    read: false,
    created_at: 1,
    updated_at: 1,
    details,
  }
}

describe('todo list categories', () => {
  test('retains the selected category after its final item is processed', () => {
    assert.deepEqual(visibleTodoCategories([], 'open_source_bounty', false), [
      'all',
      'open_source_bounty',
    ])
  })
  test('does not duplicate categories or accept unknown server keys', () => {
    const summary: TodoCategorySummary = {
      key: 'open_source_bounty',
      total: 2,
      unread: 1,
    }
    const unknown = {
      key: 'future_category',
      total: 10,
      unread: 1,
    } as unknown as TodoCategorySummary
    assert.deepEqual(
      visibleTodoCategories([summary, summary, unknown], 'all', false),
      ['all', 'open_source_bounty']
    )
  })
  test('keeps empty review queues available to administrators', () => {
    const summaries: TodoCategorySummary[] = [
      { key: 'account_action', total: 0, unread: 0 },
      { key: 'human_support', total: 0, unread: 0 },
    ]
    assert.deepEqual(visibleTodoCategories(summaries, 'all', true), [
      'all',
      'account_action',
      'human_support',
    ])
    assert.deepEqual(visibleTodoCategories(summaries, 'all', false), ['all'])
  })
})

describe('todo pagination', () => {
  for (const [total, expected] of [
    [0, 1],
    [1, 1],
    [50, 1],
    [51, 2],
    [100, 2],
    [101, 3],
  ] as const) {
    test(`${total} items require ${expected} page(s)`, () => {
      assert.equal(todoPageCount(total, 50), expected)
    })
  }
  test('handles invalid metadata without producing NaN or zero pages', () => {
    assert.equal(todoPageCount(Number.NaN, 50), 1)
    assert.equal(todoPageCount(Infinity, 50), 1)
    assert.equal(todoPageCount(-1, 50), 1)
    assert.equal(todoPageCount(51, 0), 2)
    assert.equal(todoPageCount(51, Infinity), 2)
  })
})

describe('todo dates and destinations', () => {
  test('formats valid Unix seconds and safely omits invalid dates', () => {
    assert.equal(todoTimestamp(1), '1970-01-01T00:00:01.000Z')
    for (const value of [0, -1, Number.NaN, Infinity, 9e15]) {
      assert.equal(todoTimestamp(value), undefined)
    }
  })
  test('does not advertise invalid project or request identifiers', () => {
    for (const value of [
      0,
      -1,
      0.5,
      Number.NaN,
      Infinity,
      Number.MAX_SAFE_INTEGER + 1,
    ]) {
      assert.equal(
        todoItemHasDestination(
          item('open_source_bounty', { project_id: value })
        ),
        false
      )
      assert.equal(
        todoItemHasDestination({
          ...item('developer_access'),
          source_id: value,
        }),
        false
      )
    }
    assert.equal(
      todoDetailNumber(
        item('account_action', { user_id: Infinity }),
        'user_id'
      ),
      undefined
    )
  })
  test('does not navigate ordinary users into administrative workflows', () => {
    for (const category of [
      'account_action',
      'developer_access',
      'security_incident',
      'security_review',
      'human_support',
    ] as const) {
      const notification = item(category, { username: 'customer' })
      assert.equal(todoItemCanOpen(notification, false), false)
      assert.equal(todoItemCanOpen(notification, true), true)
    }
    assert.equal(
      todoItemCanOpen(item('open_source_bounty', { project_id: 12 }), false),
      true
    )
  })
  test('keeps security reviews on the audit timeline regardless of project metadata', () => {
    const review = item('security_review', { project_id: 12 })
    assert.equal(todoSecurityReviewDestination(review), '/security')
    assert.equal(
      todoItemHasDestination(item('security_incident', { username: '   ' })),
      false
    )
  })
})
