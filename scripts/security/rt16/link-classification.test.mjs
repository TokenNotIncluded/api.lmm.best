/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// URL classification only. This does not exercise DOMPurify, React, a browser,
// persistence, authorization, or script execution.
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { stripTypeScriptTypes } from 'node:module'
import { test } from 'node:test'

const path = process.env.RT16_MARKDOWN_SOURCE ||
  new URL('../../../apps/web/src/components/ui/markdown.tsx', import.meta.url)
const source = await readFile(path, 'utf8')
const declaration = source.match(/^export function isExternalUrl\([^]*?^}/m)?.[0]
assert.ok(declaration, 'Cannot locate the actual isExternalUrl declaration')
const code = stripTypeScriptTypes(declaration)
const { isExternalUrl } = await import(
  `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
)

const cases = [
  ['empty', '', false],
  ['fragment', '#details', false],
  ['query', '?variant=plain#details', false],
  ['root-relative', '/store/items/one', false],
  ['relative', '../help', false],
  ['same origin', 'http://127.0.0.1:4189/help', false],
  ['same origin with whitespace', '  http://127.0.0.1:4189/help  ', false],
  ['different port', 'http://127.0.0.1:4190/help', true],
  ['different scheme', 'https://127.0.0.1:4189/help', true],
  ['different host', 'https://guide.example.test/help', true],
  ['scheme-relative same origin', '//127.0.0.1:4189/help', false],
  ['scheme-relative other origin', '//guide.example.test/help', true],
  ['mail link is not an HTTP origin', 'mailto:help@example.test', false],
  ['invalid URL', 'http://[', false],
]

test('classify links using the current page origin', async (t) => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'window')
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: { location: new URL('http://127.0.0.1:4189/store/product') },
  })
  try {
    for (const [name, value, expected] of cases) {
      await t.test(name, () => assert.equal(isExternalUrl(value), expected))
    }
  } finally {
    if (previous) Object.defineProperty(globalThis, 'window', previous)
    else delete globalThis.window
  }
})

test('the document-free fallback uses localhost, not an arbitrary external origin', () => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'window')
  delete globalThis.window
  try {
    assert.equal(isExternalUrl('/help'), false)
    assert.equal(isExternalUrl('http://localhost/help'), false)
    assert.equal(isExternalUrl('https://guide.example.test/help'), true)
  } finally {
    if (previous) Object.defineProperty(globalThis, 'window', previous)
  }
})
