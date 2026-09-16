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
const motion = readFileSync(
  new URL('../home/home-motion.ts', import.meta.url),
  'utf8'
)

test('homepage does not load optional GPU ornament runtimes or announce a rotating hardcoded catalog', () => {
  assert.doesNotMatch(
    source,
    /forge-liquid-accent|forge-metal-window-ornament|metal-fx|liquid-gooey/
  )
  assert.doesNotMatch(source, /HOME_MODEL_NAMES|HOME_MODEL_ROTATION_MS/)
})

test('scroll motion uses the owned lifecycle without taking over native scrolling', () => {
  assert.match(source, /return mountHomeMotion\(rootRef\.current\)/)
  assert.doesNotMatch(source, /addEventListener\(['"]scroll/)
  assert.match(
    motion,
    /document\.addEventListener\('scroll', update, \{ passive: true, capture: true \}\)/
  )
  assert.match(
    motion,
    /document\.removeEventListener\('scroll', update, true\)/
  )
  assert.match(motion, /cancelAnimationFrame\(frame\)/)
  assert.match(motion, /observer\.disconnect\(\)/)
  assert.match(motion, /resizeObserver\.disconnect\(\)/)
})

test('section progress drives the current scene and story styles', () => {
  assert.match(motion, /setProperty\('--scene-progress',/)
  assert.match(motion, /setProperty\('--story-progress',/)
  assert.match(css, /var\(--scene-progress,\s*0\)/)
  assert.match(css, /var\(--story-progress\)/)
})

test('missing observers retain a static scene instead of starting an animation loop', () => {
  assert.match(
    motion,
    /if \(!\('IntersectionObserver' in window\) \|\| !\('ResizeObserver' in window\)\) \{\s*draw\?\.\(0, \{ x: 0, y: 0 \}, 0\)\s*if \(toggle\) toggle\.hidden = true\s*return \(\) => \{\}/
  )
  assert.match(css, /\.lmm-story:not\(\[data-chapter\]\)/)
})

test('reduced motion disables animation, transitions, smooth scrolling and scene transforms', () => {
  const reducedMotion = css.match(
    /@media \(prefers-reduced-motion: reduce\) \{([\s\S]*?)\n\}/
  )?.[1]
  assert.ok(reducedMotion, 'a reduced-motion CSS override must remain')
  assert.match(reducedMotion, /animation: none !important/)
  assert.match(reducedMotion, /transition: none !important/)
  assert.match(reducedMotion, /scroll-behavior: auto !important/)
  assert.match(reducedMotion, /transform: none !important/)
  assert.match(motion, /matchMedia\('\(prefers-reduced-motion: reduce\)'\)/)
  assert.match(motion, /const animate = !reduced\.matches && !paused/)
})

test('paused and reduced-motion states keep the code surface static', () => {
  assert.match(
    css,
    /\.lmm-home\[data-motion='paused'\] \.lmm-code-surface,\s*\.lmm-home\[data-motion='reduced'\] \.lmm-code-surface \{\s*transform: none;/
  )
})
