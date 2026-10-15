/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { describe, test } from 'node:test'

const source = readFileSync(
  new URL('./users-mobile-list.tsx', import.meta.url),
  'utf8'
)

describe('UsersMobileList email presentation', () => {
  test('keeps long email addresses readable on narrow screens', () => {
    assert.match(source, /\[overflow-wrap:anywhere\]/)
    assert.match(source, /title=\{email \|\| t\('No email provided'\)\}/)
    assert.doesNotMatch(source, /mt-1 truncate text-xs/)
  })
})
