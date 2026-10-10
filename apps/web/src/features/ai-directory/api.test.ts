/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  DEFAULT_AI_DIRECTORY_LINKS,
  parseDirectoryLinks,
  safeDirectoryUrl,
} from './api'

test('ships official AI links and distinct community resources', () => {
  assert.ok(DEFAULT_AI_DIRECTORY_LINKS.some((link) => link.id === 'chatgpt'))
  assert.ok(DEFAULT_AI_DIRECTORY_LINKS.some((link) => link.id === 'gemini'))
  assert.ok(DEFAULT_AI_DIRECTORY_LINKS.some((link) => link.id === 'grok'))
  assert.ok(DEFAULT_AI_DIRECTORY_LINKS.some((link) => link.id === 'deepseek'))
  assert.ok(
    DEFAULT_AI_DIRECTORY_LINKS.some((link) => link.id === 'openai-status')
  )
  assert.ok(DEFAULT_AI_DIRECTORY_LINKS.some((link) => link.id === 'codexradar'))
  assert.ok(
    DEFAULT_AI_DIRECTORY_LINKS.every((link) => safeDirectoryUrl(link.url))
  )
})

test('rejects unsafe or malformed configured website destinations', () => {
  assert.equal(safeDirectoryUrl('javascript:alert(1)'), null)
  assert.equal(safeDirectoryUrl('https://user:secret@example.com'), null)
  const link = { ...DEFAULT_AI_DIRECTORY_LINKS[0] }
  assert.equal(
    parseDirectoryLinks(
      JSON.stringify([{ ...link, url: 'javascript:alert(1)' }])
    ),
    null
  )
  assert.equal(parseDirectoryLinks(JSON.stringify([link, link])), null)
})

test('empty configuration uses presets while an explicit empty list hides them', () => {
  assert.equal(parseDirectoryLinks(''), DEFAULT_AI_DIRECTORY_LINKS)
  assert.deepEqual(parseDirectoryLinks('[]'), [])
})
