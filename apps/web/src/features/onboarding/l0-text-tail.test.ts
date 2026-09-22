/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/

import assert from 'node:assert/strict'
import { test } from 'node:test'

import { visualTokens, visualTokenTail } from './l0-text-flow'

test('a bounded tail preserves exact offsets and never splits joined Unicode', () => {
  for (const text of [
    '',
    'short',
    `${'a'.repeat(250_000)}你好👨‍👩‍👧é🇨🇳`,
    `e${'\u0301'.repeat(1800)}${'🎉'.repeat(250)}`,
  ]) {
    const expected = visualTokens(text).slice(-192)
    const tail = visualTokenTail(text)
    assert.deepEqual(tail, expected)
    assert.equal(
      text.slice(0, tail[0]?.index ?? 0) +
        tail.map((token) => token.text).join(''),
      text
    )
    assert.ok(tail.length <= 192)
  }
  assert.deepEqual(visualTokenTail('hello', 0), [])
})
