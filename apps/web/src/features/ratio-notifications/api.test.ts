/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { TFunction } from 'i18next'

import { api } from '@/lib/api'

import { listRatioNotifications, ratioAnnouncement } from './api'

test('ratio events preserve zero, represent null as default, and use account-specific read IDs', () => {
  const t = ((key: string, options?: { time?: string }) =>
    key.replace('{{time}}', options?.time ?? '')) as TFunction
  const event = {
    event_id: 'batch',
    effective_at: 1789171200,
    changes: [
      { option: 'GroupRatio', group: '<img src=x>', old: null, new: 0 },
    ],
  }
  const item = ratioAnnouncement(event, 1, t)
  assert.match(item.content, /Old value: Default → New value: 0/)
  assert.equal(item.plainText, true)
  assert.match(item.extra, /2026-09-12T00:00:00.000Z/)
  assert.notEqual(item.id, ratioAnnouncement(event, 2, t).id)
})

test('reads only the scoped feed and passes the backend pagination cursor', async () => {
  const original = api.get
  const calls: unknown[] = []
  api.get = (async (url: string, config: unknown) => {
    calls.push([url, config])
    return { data: { success: true, data: [], next: 'cursor-50' } }
  }) as typeof api.get
  try {
    assert.deepEqual(await listRatioNotifications('cursor-100'), {
      events: [],
      next: 'cursor-50',
    })
    assert.deepEqual(calls, [
      [
        '/api/ratio-notifications',
        {
          params: { before: 'cursor-100' },
          skipBusinessError: true,
          skipErrorHandler: true,
        },
      ],
    ])
    for (const status of [403, 404]) {
      api.get = (async () => {
        throw { isAxiosError: true, response: { status } }
      }) as typeof api.get
      assert.deepEqual(await listRatioNotifications(), {
        events: [],
        next: undefined,
      })
    }
  } finally {
    api.get = original
  }
})
