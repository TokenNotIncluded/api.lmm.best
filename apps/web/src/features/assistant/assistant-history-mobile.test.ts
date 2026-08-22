/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { describe, test } from 'node:test'

const source = readFileSync(
  new URL('./assistant-history.tsx', import.meta.url),
  'utf8'
)

describe('assistant history mobile controls', () => {
  test('keeps history actions and inputs reachable at narrow touch sizes', () => {
    assert.match(
      source,
      /const historyTouchTargetClassName = 'min-h-11 sm:min-h-7'/
    )
    assert.match(source, /const historyInputClassName = 'h-11 sm:h-8'/)
    assert.ok(
      (source.match(/historyTouchTargetClassName/g) ?? []).length >= 10,
      'history actions should reuse the mobile touch target class'
    )
    assert.ok(
      (source.match(/historyInputClassName/g) ?? []).length >= 3,
      'history audit inputs should use the mobile input height'
    )
    assert.match(
      source,
      /<summary className='text-muted-foreground min-h-11 cursor-pointer py-2 sm:min-h-0 sm:py-0'>/
    )
  })
})
