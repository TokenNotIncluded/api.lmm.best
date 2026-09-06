/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { describe, test } from 'node:test'

const source = readFileSync(new URL('./nav-group.tsx', import.meta.url), 'utf8')

describe('sidebar navigation interaction contract', () => {
  test('routes model-panel interactions to the client-side panel instead of navigation', () => {
    assert.match(
      source,
      /navItem\.interaction === 'model-panel' \? openPanel : undefined/
    )
    assert.match(source, /if \(onModelPanelClick\) \{/)
    assert.match(source, /event\.preventDefault\(\)/)
  })
})
