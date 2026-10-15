/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { legacyUserIDsToStrings } from './legacy-user-ids'

test('preserves exact safe legacy IDs, order and duplicates without mutating input', () => {
  const ids = Object.freeze([Number.MAX_SAFE_INTEGER, 7, 7, 42])
  assert.deepEqual(legacyUserIDsToStrings(ids), [
    '9007199254740991',
    '7',
    '7',
    '42',
  ])
  assert.deepEqual(ids, [Number.MAX_SAFE_INTEGER, 7, 7, 42])
  assert.deepEqual(legacyUserIDsToStrings([]), [])
})

test('rejects the whole selection for any unsafe or nonpositive numeric ID', () => {
  for (const id of [
    0,
    -0,
    -1,
    1.5,
    Number.NaN,
    Number.POSITIVE_INFINITY,
    Number.NEGATIVE_INFINITY,
    Number.MAX_SAFE_INTEGER + 1,
    2 ** 63,
  ]) {
    assert.equal(legacyUserIDsToStrings([7, id, 42]), undefined)
  }
})

test('never coerces v2 strings or other scalar types into legacy numbers', () => {
  for (const id of [
    '7',
    '9007199254740993',
    '9223372036854775807',
    '01',
    '+7',
    '7.0',
    '7e0',
    ' 7 ',
    '',
    7n,
    true,
    false,
    null,
    undefined,
    Symbol('synthetic-id'),
  ]) {
    assert.equal(legacyUserIDsToStrings([7, id, 42]), undefined)
  }
})

test('does not invoke object coercion and rejects missing array entries', () => {
  let conversions = 0
  const id = {
    valueOf: () => {
      conversions++
      return 7
    },
    toString: () => {
      conversions++
      return '7'
    },
  }
  assert.equal(legacyUserIDsToStrings([7, id]), undefined)
  assert.equal(conversions, 0)
  const sparse: unknown[] = []
  sparse.length = 2
  sparse[0] = 7
  assert.equal(legacyUserIDsToStrings(sparse), undefined)
})
