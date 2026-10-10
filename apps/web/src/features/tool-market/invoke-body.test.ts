/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { marketInvokeBody } from './invoke-body'

test('advanced arguments preserve integer and decimal representations in the HTTP body', () => {
  const raw =
    '{"id":9007199254740993,"maximum":9223372036854775807,"minimum":-9223372036854775808,"amount":1.2300}'
  const body = marketInvokeBody({
    request_id: 'one',
    arguments: JSON.parse(raw),
    arguments_json: raw,
  })
  assert.equal(body, `{"request_id":"one","arguments":${raw}}`)
  assert.equal(String(body).includes('arguments_json'), false)
})

test('confirmation preserves the original arguments and request ID', () => {
  const raw = '{"amount":9007199254740993}'
  const body = marketInvokeBody({
    request_id: 'same-request',
    arguments: JSON.parse(raw),
    arguments_json: raw,
    request_state: 'opaque-state',
    input_responses: { confirmation: { action: 'cancel' } },
  })
  assert.equal(typeof body, 'string')
  assert.equal(String(body).endsWith(`"arguments":${raw}}`), true)
  const parsed = JSON.parse(String(body))
  assert.equal(parsed.request_id, 'same-request')
  assert.equal(parsed.request_state, 'opaque-state')
  assert.deepEqual(parsed.input_responses, {
    confirmation: { action: 'cancel' },
  })
})

test('raw arguments reject invalid JSON, non-objects and oversized input before sending', () => {
  for (const raw of [
    '{broken',
    '{}},"request_id":"other"',
    '[]',
    'null',
    '42',
  ]) {
    assert.throws(() =>
      marketInvokeBody({ arguments: {}, arguments_json: raw })
    )
  }
  assert.throws(() =>
    marketInvokeBody({
      arguments: {},
      arguments_json: JSON.stringify({ value: 'x'.repeat(128 * 1024) }),
    })
  )
})

test('callers without raw arguments retain the ordinary structured body', () => {
  assert.deepEqual(
    marketInvokeBody({ request_id: 'one', arguments: { enabled: false } }),
    {
      request_id: 'one',
      arguments: { enabled: false },
    }
  )
})
