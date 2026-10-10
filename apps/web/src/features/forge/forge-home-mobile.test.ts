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

test('homepage exposes a keyboard-accessible interactive explore console', () => {
  assert.match(source, /lmm-explore-console/)
  assert.match(
    source,
    /data-preview-active=\{\s*activeExplore === (?:id|destination\.id)/
  )
  assert.doesNotMatch(source, /aria-current=\{\s*activeExplore/)
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
})

test('homepage presents all OAuth client options', () => {
  assert.match(source, /<strong>Pi<\/strong>/)
  assert.match(source, /<strong>DSH<\/strong>/)
  assert.match(source, /<strong>Codewhale<\/strong>/)
  assert.match(source, /DSH_WEB_INSTALL_PORTABLE_COMMAND/)
  assert.match(providerCommands, /dsh plugin --profile web add/)
  assert.match(providerCommands, /codewhale-lmm-provider\.git/)
})

test('connection controls expose both OAuth and API key choices', () => {
  assert.match(landingSource, /className='lmm-connection-method'/)
  assert.match(landingSource, /aria-pressed=\{connectionMethod === 'oauth'\}/)
  assert.match(landingSource, /aria-pressed=\{connectionMethod === 'api-key'\}/)
  assert.match(css, /\.lmm-connection-method \[aria-pressed='true'\]/)
})

test('the assistant keeps one visible underline instead of nested focus boxes', () => {
  const focus = css.match(
    /\.lmm-home \.forge-home-input:focus-within \{([\s\S]*?)\n\}/
  )?.[1]
  assert.ok(focus)
  assert.match(focus, /outline: 0;/)
  assert.match(focus, /box-shadow: 0 1px 0 var\(--foreground\);/)
  assert.match(
    css,
    /\.lmm-home \.forge-home-input input:focus-visible \{\s*outline: 0;/
  )
})
