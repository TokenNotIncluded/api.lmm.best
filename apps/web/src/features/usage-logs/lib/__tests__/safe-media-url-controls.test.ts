/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { safeMediaUrl } from '../async-task-logs'

test('media URLs reject every embedded ASCII control and backslash', () => {
  const codes = [...Array.from({ length: 32 }, (_, code) => code), 127, 92]
  for (const code of codes) {
    const character = String.fromCodePoint(code)
    for (const prefix of ['https://example.test/', '/media/']) {
      assert.equal(safeMediaUrl(`${prefix}a${character}b.mp4`), undefined)
    }
  }
})

test('media URL character checks retain ordinary Unicode and URL policy', () => {
  assert.equal(safeMediaUrl('/media/影片.mp4'), '/media/影片.mp4')
  assert.equal(
    safeMediaUrl('https://example.test/a.mp4'),
    'https://example.test/a.mp4'
  )
  assert.equal(safeMediaUrl('javascript:alert(1)'), undefined)
  assert.equal(
    safeMediaUrl('https://user:password@example.test/a.mp4'),
    undefined
  )
  assert.equal(safeMediaUrl('//example.test/a.mp4'), undefined)
})
