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

const source = (file: string) =>
  readFileSync(new URL(`./${file}`, import.meta.url), 'utf8')

describe('mobile user actions', () => {
  test('keeps row actions reachable at touch sizes', () => {
    assert.match(
      source('data-table-row-actions.tsx'),
      /className='size-11 sm:size-7'/
    )
    assert.match(
      source('user-assistant-history-dialog.tsx'),
      /className='min-h-11 sm:min-h-7'/
    )
    assert.match(
      source('user-assistant-review-dialog.tsx'),
      /className='min-h-11 sm:min-h-7'/
    )
    assert.match(
      source('users-primary-buttons.tsx'),
      /className='min-h-11 sm:min-h-7'/
    )
  })
})
