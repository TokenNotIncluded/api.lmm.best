/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { afterEach, describe, test } from 'node:test'

import { api } from '@/lib/api'

import { getTodos, markAllTodosRead, markTodoRead, type TodoItem } from './api'

const originalGet = api.get
const originalPost = api.post

afterEach(() => {
  api.get = originalGet
  api.post = originalPost
})

describe('unified to-do API', () => {
  test('loads the submitted challenge review category', async () => {
    let requestedURL = ''
    api.get = (async (url) => {
      requestedURL = url
      return {
        data: {
          success: true,
          data: {
            items: [],
            page: 1,
            page_size: 50,
            total: 0,
            category: 'open_source_bounty_review',
            unread_count: 0,
            total_unread_count: 0,
            unread_by_category: {},
            categories: [],
          },
        },
      }
    }) as typeof api.get
    await getTodos('open_source_bounty_review')
    assert.equal(
      requestedURL,
      '/api/todos?category=open_source_bounty_review&p=1&page_size=50'
    )
  })

  test('forwards the requested page and cancellation signal', async () => {
    const controller = new AbortController()
    let requestedURL = ''
    api.get = (async (url, config) => {
      requestedURL = url
      assert.equal(config?.signal, controller.signal)
      return { data: { success: true, data: { page: 2 } } }
    }) as typeof api.get
    const result = await getTodos('all', 2, controller.signal)
    assert.equal(requestedURL, '/api/todos?category=all&p=2&page_size=50')
    assert.equal(result.page, 2)
  })

  test('rejects invalid pages before making a request', () => {
    let calls = 0
    api.get = (() => {
      calls += 1
    }) as unknown as typeof api.get
    for (const page of [
      0,
      -1,
      1.5,
      Number.NaN,
      Infinity,
      Number.MAX_SAFE_INTEGER + 1,
    ]) {
      assert.throws(() => getTodos('all', page), RangeError)
    }
    assert.equal(calls, 0)
  })

  test('preserves API envelope failures instead of treating them as empty results', async () => {
    api.get = (async () => ({
      data: { success: false, message: 'Access denied' },
    })) as typeof api.get
    await assert.rejects(getTodos('all'), /Access denied/)
  })

  test('preserves read failures for callers to recover from', async () => {
    api.post = (async () => ({
      data: { success: false, message: 'Try again' },
    })) as typeof api.post
    await assert.rejects(markAllTodosRead(), /Try again/)
  })

  test('marks only the visible source item or all categories explicitly', async () => {
    const posts: Array<{ url: string; body: unknown }> = []
    api.post = (async (url, body) => {
      posts.push({ url, body })
      return { data: { success: true, data: { marked: 1 } } }
    }) as typeof api.post
    const item = {
      id: 'open_source_bounty_review:12',
      source_id: 12,
      category: 'open_source_bounty_review',
      type: 'challenge_submitted',
      title: 'open_source_bounty.challenge_submitted',
      summary: 'Review this fix',
      read: false,
      created_at: 1,
      updated_at: 1,
    } satisfies TodoItem
    await markTodoRead(item)
    await markAllTodosRead()
    assert.deepEqual(posts, [
      {
        url: '/api/todos/read',
        body: { category: 'open_source_bounty_review', ids: [12], all: false },
      },
      { url: '/api/todos/read', body: { category: 'all', ids: [], all: true } },
    ])
  })
})
