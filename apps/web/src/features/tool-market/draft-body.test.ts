/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { DraftInput } from './api'
import { marketDraftBody, marketSchemaJSON } from './draft-body'

const raw =
  '{"type":"object","properties":{"id":{"type":"integer","minimum":9007199254740993,"maximum":9223372036854775807,"default":9007199254740993}}}'
const output =
  '{"type":"object","properties":{"count":{"type":"integer","default":9223372036854775807}}}'
const draft: DraftInput = {
  name: 'Precise tools',
  description: '',
  endpoint: 'https://tools.example.test/mcp',
  execution_type: 'remote',
  visibility: 'private',
  allowed_users: [],
  tools: [
    {
      name: 'precise',
      description: '',
      input_schema: raw,
      output_schema: output,
      permissions: ['read'],
      price_quota: 0,
    },
  ],
}

test('draft schema raw JSON is embedded as an object without rounding bounds or defaults', () => {
  const body = marketDraftBody(draft)
  assert.equal(body.includes(`"input_schema":${raw}`), true)
  assert.equal(body.includes(`"output_schema":${output}`), true)
  assert.equal(body.includes('input_schema_json'), false)
  assert.equal(typeof JSON.parse(body).tools[0].input_schema, 'object')
})

test('discovery raw schema fields win over rounded legacy presentation copies', () => {
  const body = marketDraftBody({
    ...draft,
    tools: [
      {
        ...draft.tools[0],
        input_schema: JSON.parse(raw),
        input_schema_json: raw,
        output_schema: JSON.parse(output),
        output_schema_json: output,
      },
    ],
  })
  assert.equal(body.includes(`"input_schema":${raw}`), true)
  assert.equal(body.includes(`"output_schema":${output}`), true)
  assert.equal(body.includes('input_schema_json'), false)
  assert.equal(body.includes('output_schema_json'), false)
})

test('legacy parsed schema with unsafe integers is rejected instead of silently changed', () => {
  assert.throws(() => marketSchemaJSON(JSON.parse(raw)), /original JSON/)
  assert.throws(
    () => marketSchemaJSON({ enum: [9007199254740992] }),
    /original JSON/
  )
  assert.equal(
    marketSchemaJSON({ type: 'object', minimum: 42 }),
    '{"type":"object","minimum":42}'
  )
})

test('raw schema cannot close its JSON value or replace draft identity', () => {
  for (const value of ['{}},"endpoint":"other"', '{broken', 'null', '[]']) {
    assert.throws(() => marketSchemaJSON(value))
  }
  assert.equal(marketSchemaJSON(raw), raw)
})
