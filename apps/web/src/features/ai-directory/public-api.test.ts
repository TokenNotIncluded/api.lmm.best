/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

import { DEFAULT_AI_DIRECTORY_LINKS, getAIDirectory } from './api'
import { getPublicDirectory } from './public-api'

test('directory reads omit credentials and use the configured public list', async () => {
  const previousFetch = globalThis.fetch
  const links = [{ ...DEFAULT_AI_DIRECTORY_LINKS[0], name: 'Configured AI' }]
  globalThis.fetch = async (input, init) => {
    assert.equal(input, '/api/ai-directory')
    assert.equal(init?.method, 'GET')
    assert.equal(init?.credentials, 'omit')
    assert.deepEqual(init?.headers, { Accept: 'application/json' })
    assert.ok(init?.signal instanceof AbortSignal)
    return Response.json({ success: true, data: { links } })
  }
  try {
    assert.deepEqual(await getAIDirectory(), links)
  } finally {
    globalThis.fetch = previousFetch
  }
})

test('public ad pagination also omits account credentials', async () => {
  const previousFetch = globalThis.fetch
  const data = { success: true, data: { items: [], has_more: false } }
  globalThis.fetch = async (input, init) => {
    assert.equal(input, '/api/ai-directory/ads?offset=20')
    assert.equal(init?.credentials, 'omit')
    assert.deepEqual(init?.headers, { Accept: 'application/json' })
    return Response.json(data)
  }
  try {
    assert.deepEqual(
      await getPublicDirectory('/api/ai-directory/ads?offset=20'),
      { data }
    )
  } finally {
    globalThis.fetch = previousFetch
  }
})

test('public request failures reject without refreshing or redirecting', async () => {
  const previousFetch = globalThis.fetch
  let calls = 0
  globalThis.fetch = async () => {
    calls += 1
    return new Response(null, { status: 401 })
  }
  try {
    await assert.rejects(getAIDirectory(), /Unable to load AI directory/)
    assert.equal(calls, 1)
  } finally {
    globalThis.fetch = previousFetch
  }
})

test('advertising and management keep their authenticated client', () => {
  const source = readFileSync(new URL('./ads-api.ts', import.meta.url), 'utf8')
  assert.match(source, /getPublicDirectory\(`/)
  assert.match(source, /api.get\(`\/api\/ai-directory\/ads\/quote/)
  assert.match(source, /api.get\('\/api\/ai-directory\/ads\/mine'/)
  assert.match(source, /api.post\('\/api\/ai-directory\/ads'/)
  assert.match(source, /api.post\(`\/api\/ai-directory\/ads\/\$\{id\}\/hide/)
})

test('guests cannot mount the payment dialog and sign-in returns to the directory', () => {
  const source = readFileSync(
    new URL('./sponsored-section.tsx', import.meta.url),
    'utf8'
  )
  assert.match(source, /canPromote && (?:\(\s*)?<AdvertisementDialog/)
  assert.match(source, /isSignedIn && isConsoleActivated\(auth.user\)/)
  assert.match(
    source,
    /to='\/sign-in' search=\{\{ redirect: '\/ai-directory' \}\}/
  )
})
