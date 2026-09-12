/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const source = readFileSync(
  new URL('./forge-home.tsx', import.meta.url),
  'utf8'
)
const css = readFileSync(new URL('./forge-home.css', import.meta.url), 'utf8')

test('homepage does not load optional GPU ornament runtimes or announce a rotating hardcoded catalog', () => {
  assert.doesNotMatch(
    source,
    /forge-liquid-accent|forge-metal-window-ornament|metal-fx|liquid-gooey/
  )
  assert.doesNotMatch(source, /HOME_MODEL_NAMES|HOME_MODEL_ROTATION_MS/)
})

test('scroll animation is progressive and has a reduced-motion fallback', () => {
  assert.match(css, /@supports \(animation-timeline: view\(\)\)/)
  assert.match(css, /prefers-reduced-motion: reduce/)
  assert.doesNotMatch(source, /addEventListener\(['"]scroll/)
})
