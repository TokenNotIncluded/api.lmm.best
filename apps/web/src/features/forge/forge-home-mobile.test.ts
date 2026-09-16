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

test('the homepage owns and cleans up its progressive motion enhancement', () => {
  assert.match(source, /return mountHomeMotion\(rootRef\.current\)/)
  assert.doesNotMatch(source, /addEventListener\(['"]scroll/)
})

test('reduced motion disables animation, transitions and transformed surfaces', () => {
  const reducedMotion = css.match(
    /@media \(prefers-reduced-motion: reduce\) \{([\s\S]*?)\n\}/
  )?.[1]
  assert.ok(reducedMotion)
  assert.match(reducedMotion, /animation: none !important/)
  assert.match(reducedMotion, /transition: none !important/)
  assert.match(reducedMotion, /scroll-behavior: auto !important/)
  assert.match(reducedMotion, /transform: none !important/)
  assert.match(
    css,
    /\.lmm-story:not\(\[data-chapter\]\) \[data-story-panel='2'\]/
  )
})
