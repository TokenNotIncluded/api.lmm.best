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

test('schema defaults retain exact numbers for optional and required arguments', () => {
  const definition = `{
    "type": "object",
    "properties": {
      "optional": {"type":"integer","minimum":9007199254740993,"default":9007199254740993},
      "required": {"type":"integer","default":9007199254740995},
      "nested": {"type":"object","default":{"items":[9007199254740997,1.0000000000000000000000000001]}},
      "query": {"type":"string","default":"hi"},
      "confirmed": {"type":"boolean","default":true}
    },
    "required": ["required"]
  }`
  const defaults = initialArguments(definition)
  assert.match(defaults, /"optional": 9007199254740993/)
  assert.match(defaults, /"required": 9007199254740995/)
  assert.match(defaults, /\[9007199254740997,1\.0000000000000000000000000001\]/)
  assert.equal(JSON.parse(defaults).query, 'hi')
  assert.equal(Object.hasOwn(JSON.parse(defaults), 'confirmed'), false)
  assert.equal(argumentIssue(defaults, definition), null)
})

test('editing a different parameter retains advanced numeric tokens at every depth', () => {
  const raw = String.raw`{
    "count": 9007199254740993,
    "precise": 1.0000000000000000000000000001,
    "exponent": 123456789012345678901e100,
    "nested": {"items":[9007199254740995,{"decimal":0.123456789012345678901}]},
    "\u005f_proto__": {"value":9007199254740997},
    "text": "comma, quote\" and closing } ] :",
    "query": "old"
  }`
  const updated = updateArgument(raw, 'query', 'new')
  assert.match(updated, /"count": 9007199254740993/)
  assert.match(updated, /"precise": 1\.0000000000000000000000000001/)
  assert.match(updated, /"exponent": 123456789012345678901e100/)
  assert.match(
    updated,
    /"nested": \{"items":\[9007199254740995,\{"decimal":0\.123456789012345678901\}\]\}/
  )
  assert.match(updated, /"__proto__": \{"value":9007199254740997\}/)
  assert.equal(JSON.parse(updated).query, 'new')
  assert.equal(JSON.parse(updated).text, 'comma, quote" and closing } ] :')
  const cleared = updateArgument(updated, 'count', undefined)
  assert.equal(Object.hasOwn(JSON.parse(cleared), 'count'), false)
  assert.match(cleared, /9007199254740995/)
})

test('guided numeric drafts do not silently round high precision decimal input', () => {
  assert.equal(
    parameterValue({ type: 'number' }, '1.0000000000000000000000000001'),
    '1.0000000000000000000000000001'
  )
  assert.equal(parameterValue({ type: 'number' }, '.25'), 0.25)
  assert.equal(parameterValue({ type: 'number' }, '+1.25e2'), 125)
  assert.equal(parameterValue({ type: 'number' }, '1.2500'), 1.25)
})

test('empty, duplicate and escaped object properties keep JSON object semantics', () => {
  assert.equal(updateArgument('{}', 'query', 'new'), '{\n  "query": "new"\n}')
  const updated = updateArgument(
    '{"query":"first","\\u0071uery":"last","constructor":{"n":9007199254740993}}',
    'enabled',
    false
  )
  assert.equal(JSON.parse(updated).query, 'last')
  assert.match(updated, /"constructor": \{"n":9007199254740993\}/)
  assert.throws(() => updateArgument('[]', 'query', 'new'), /must be an object/)
  assert.throws(() => updateArgument('{broken', 'query', 'new'))
})
