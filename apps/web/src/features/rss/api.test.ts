/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { parseRSSFeeds, safeRSSFeedUrl } from './api'

test('RSS feed configuration accepts safe HTTP feeds', () => {
  const feeds = parseRSSFeeds(
    JSON.stringify([
      {
        id: 'release-notes',
        name: 'Release notes',
        url: 'https://example.com/feed.xml',
        enabled: true,
      },
    ])
  )
  assert.equal(feeds?.length, 1)
  assert.equal(feeds?.[0]?.name, 'Release notes')
})

test('RSS feed configuration rejects duplicate and credentialed URLs', () => {
  assert.equal(
    parseRSSFeeds(
      JSON.stringify([
        {
          id: 'a',
          name: 'A',
          url: 'https://example.com/feed.xml',
          enabled: true,
        },
        {
          id: 'b',
          name: 'B',
          url: 'https://example.com/feed.xml',
          enabled: true,
        },
      ])
    ),
    null
  )
  assert.equal(safeRSSFeedUrl('https://user:pass@example.com/feed.xml'), null)
  assert.equal(safeRSSFeedUrl('file:///tmp/feed.xml'), null)
})
