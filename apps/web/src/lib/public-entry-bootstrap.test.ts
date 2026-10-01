/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { bootstrapPublicEntry } from './public-entry-bootstrap'

test('public entries render while authentication remains unresolved', async () => {
  for (const pathname of [
    '/scripts',
    '/scripts/',
    '/scripts/install.sh',
    '/test-key',
    '/test-key/',
  ]) {
    let calls = 0
    let finish: () => void = () => {}
    const pending = new Promise<void>((resolve) => {
      finish = resolve
    })
    assert.equal(
      bootstrapPublicEntry(pathname, () => {
        calls++
        return pending
      }),
      true
    )
    assert.equal(calls, 1)
    // The decision returns before the session resolves; consumers can render.
    finish()
    await pending
  }
})

test('unavailable authentication does not reject public navigation', async () => {
  assert.equal(
    bootstrapPublicEntry('/scripts', async () => {
      throw new Error('API unavailable')
    }),
    true
  )
  await new Promise((resolve) => setTimeout(resolve, 0))
})

test('protected routes and similar prefixes keep normal bootstrap checks', () => {
  for (const pathname of [
    '/keys',
    '/channels',
    '/setup',
    '/scripts-private',
    '/test-key-admin',
    '/test-key/nested',
    '/api/scripts/repository',
  ]) {
    assert.equal(
      bootstrapPublicEntry(pathname, async () => {
        throw new Error('must not be called')
      }),
      false
    )
  }
})
