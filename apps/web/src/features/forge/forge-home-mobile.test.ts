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
const landingSource = readFileSync(
  new URL('../home/home-landing.tsx', import.meta.url),
  'utf8'
)
const providerCommands = readFileSync(
  new URL('../guide/provider-install-commands.ts', import.meta.url),
  'utf8'
)
const css = readFileSync(new URL('./forge-home.css', import.meta.url), 'utf8')
const motion = readFileSync(
  new URL('../home/home-motion.ts', import.meta.url),
  'utf8'
)

test('homepage exposes a keyboard-accessible interactive explore console', () => {
  assert.match(source, /lmm-explore-console/)
  assert.match(
    source,
    /aria-current=\{\s*activeExplore === (?:id|destination\.id)/
  )
  assert.match(
    source,
    /onFocus=\{\(\) => setActiveExplore\((?:id|destination\.id)\)\}/
  )
  assert.match(css, /\.lmm-explore-console/)
  assert.match(css, /@media \(max-width: 680px\)/)
})

test('homepage does not load optional GPU ornament runtimes or announce a rotating hardcoded catalog', () => {
  assert.doesNotMatch(
    source,
    /forge-liquid-accent|forge-metal-window-ornament|metal-fx|liquid-gooey/
  )
  assert.doesNotMatch(source, /HOME_MODEL_NAMES|HOME_MODEL_ROTATION_MS/)
})

test('the homepage owns and cleans up its progressive motion enhancement', () => {
  assert.match(source, /import\('@\/features\/home\/home-motion'\)/)
  assert.match(source, /release = mountHomeMotion\(root\)/)
  assert.match(source, /disposed = true\s+release\(\)/)
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

test('scroll motion retains passive listeners, cancellation and observer cleanup', () => {
  assert.match(
    motion,
    /document\.addEventListener\('scroll', scrollScene, \{\s*passive: true,?\s*capture: true,?\s*\}\)/
  )
  assert.match(
    motion,
    /document\.removeEventListener\('scroll', scrollScene, true\)/
  )
  assert.match(motion, /cancelAnimationFrame\(frame\)/)
  assert.match(motion, /observer\.disconnect\(\)/)
  assert.match(motion, /resizeObserver\.disconnect\(\)/)
})

test('section progress drives the current scene and story styles', () => {
  assert.match(motion, /setProperty\('--scene-progress',/)
  assert.match(motion, /setProperty\('--story-progress',/)
  assert.match(
    motion,
    /const time = reduced\.matches \? RESPONSE_END : clock\s+draw\(time, pointer, sceneProgress\)/
  )
  assert.match(css, /var\(--story-progress\)/)
})

test('missing observer constructors preserve static content', () => {
  assert.match(motion, /typeof window\.IntersectionObserver !== 'function'/)
  assert.match(motion, /typeof window\.ResizeObserver !== 'function'/)
  // The lifecycle tests execute this fallback and its cleanup, including
  // decorations; do not require an exact ordering of implementation statements.
  assert.match(css, /\.lmm-story:not\(\[data-chapter\]\)/)
})

test('paused and reduced-motion states keep the code surface static', () => {
  assert.match(
    css,
    /\.lmm-home\[data-motion='paused'\] \.lmm-code-surface,\s*\.lmm-home\[data-motion='reduced'\] \.lmm-code-surface \{\s*transform: none;/
  )
  assert.match(motion, /matchMedia\('\(prefers-reduced-motion: reduce\)'\)/)
  assert.match(motion, /const animate = !reduced\.matches && !paused/)
})

test('homepage removes the manual word field and presents all OAuth client options', () => {
  assert.doesNotMatch(landingSource, /Type a word\. See what connects\./)
  assert.doesNotMatch(landingSource, /data-home-token-field/)
  assert.match(source, /<strong>Pi<\/strong>/)
  assert.match(source, /<strong>DSH<\/strong>/)
  assert.match(source, /<strong>Codewhale<\/strong>/)
  assert.match(source, /DSH_WEB_INSTALL_PORTABLE_COMMAND/)
  assert.match(providerCommands, /dsh plugin --profile web add/)
  assert.match(providerCommands, /codewhale-lmm-provider\.git/)
})
