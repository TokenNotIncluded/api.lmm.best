/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  argumentIssue,
  guidedParameters,
  initialArguments,
  parameterValue,
  readParameterSchema,
  updateArgument,
} from './schema-form-utils'

const schema = JSON.stringify({
  type: 'object',
  properties: {
    query: { type: 'string', minLength: 2 },
    count: { type: 'integer', minimum: 1, maximum: 3 },
    enabled: { type: 'boolean' },
    mode: { type: 'string', enum: ['read', 'write'] },
  },
  required: ['query'],
})

test('guided parameters validate required fields and preserve exact primitive types', () => {
  assert.equal(argumentIssue('{}', schema), 'Missing parameter: query')
  assert.equal(
    argumentIssue(
      '{"query":"hi","count":2,"enabled":false,"mode":"read"}',
      schema
    ),
    null
  )
  for (const bad of [
    { query: 'x' },
    { query: 'hi', count: 1.5 },
    { query: 'hi', count: 4 },
    { query: 'hi', enabled: 'false' },
    { query: 'hi', mode: 'other' },
  ]) {
    assert.match(
      argumentIssue(JSON.stringify(bad), schema) ?? '',
      /Invalid parameter/
    )
  }
  assert.equal(parameterValue({ type: 'integer' }, '2'), 2)
  assert.equal(
    parameterValue({ type: 'integer' }, '9007199254740993'),
    '9007199254740993'
  )
  assert.equal(parameterValue({ type: 'integer' }, ''), undefined)
  assert.equal(parameterValue({ type: 'string' }, 'false'), 'false')
})

test('updates preserve advanced properties, false values and prototype-named JSON keys', () => {
  const raw =
    '{"nested":{"items":[1,2]},"enabled":false,"__proto__":{"polluted":true}}'
  const updated = JSON.parse(updateArgument(raw, 'query', 'hi'))
  assert.deepEqual(updated.nested, { items: [1, 2] })
  assert.equal(updated.enabled, false)
  assert.deepEqual(updated.__proto__, { polluted: true })
  assert.equal(({} as Record<string, unknown>).polluted, undefined)
  assert.equal(
    Object.hasOwn(
      JSON.parse(updateArgument(raw, 'enabled', undefined)),
      'enabled'
    ),
    false
  )
})

test('advanced and malformed schema input remains available without invented confirmation', () => {
  assert.equal(readParameterSchema('{broken'), null)
  assert.deepEqual(
    guidedParameters(readParameterSchema('{"$ref":"#/$defs/arguments"}')),
    []
  )
  assert.equal(argumentIssue('{broken', schema), 'Enter valid JSON.')
  assert.equal(argumentIssue('[]', schema), 'Arguments must be a JSON object.')
  const defaults = JSON.parse(
    initialArguments(
      '{"type":"object","properties":{"query":{"type":"string","default":"hi"},"confirmed":{"type":"boolean","default":true}}}'
    )
  )
  assert.deepEqual(defaults, { query: 'hi' })
})
