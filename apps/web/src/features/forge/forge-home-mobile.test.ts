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
  new URL('./forge-home.tsx', import.meta.url),
  'utf8'
)

describe('Forge home mobile controls', () => {
  test('keeps the centered hero and compact assistant submit control', () => {
    assert.match(source, /<section className='forge-home-hero'/)
    assert.match(source, /className='forge-home-hero-content'/)
    assert.match(source, /const HOME_MODEL_NAMES = \[/)
    assert.match(source, /setInterval\(\(\) => \{/)
    assert.match(source, /className='forge-home-model-current'/)
    assert.match(source, /modelMeasureRef/)
    assert.match(source, /getBoundingClientRect\(\)\.width/)
    assert.match(source, /style=\{modelWidth \?/)
    assert.match(
      source,
      /<InputGroupButton[\s\S]*className='h-10 rounded-full px-4'/
    )
  })
})
