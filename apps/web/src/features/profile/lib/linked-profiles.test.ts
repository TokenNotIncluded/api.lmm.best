/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  prepareLinkedProfiles,
  profileDraft,
  validProfileURL,
} from './linked-profiles'

const now = new Date('2026-10-04T08:15:42Z')
const snapshot = () => ({
  ...profileDraft(
    {
      provider: 'chatgpt',
      url: 'https://chatgpt.com/u/one',
      snapshot: {
        tokens: 117_000_000_000,
        period: 'all',
        approximate: true,
        observed_at: now.toISOString(),
        source: 'Codex lifetime tokens; owner-observed snapshot',
      },
    },
    now
  ),
})

test('provider URLs match canonical identity and permit public custom profiles', () => {
  assert.equal(
    validProfileURL('cursor', 'https://cursor.com/@lightjunction'),
    true
  )
  assert.equal(
    validProfileURL('chatgpt', 'https://chatgpt.com/u/lightjunction.me'),
    true
  )
  for (const url of [
    'https://github.com/lightjunction',
    'https://huggingface.co/lightjunction',
  ]) {
    assert.equal(validProfileURL('custom', url), true)
  }
  for (const url of [
    'https://cursor.com/@one/',
    'https://CURSOR.com/@one',
    'HTTPS://cursor.com/@one',
    'https://cursor.com/@-one',
    `https://cursor.com/@${'a'.repeat(81)}`,
    'https://cursor.com/@one?',
    'https://cursor.com/@one#',
    'https://cursor.com/@%6fne',
    'https://cursor.com:443/@one',
    'https://one:secret@cursor.com/@one',
    'https://cursor.com/@one/../two',
  ]) {
    assert.equal(validProfileURL('cursor', url), false, url)
  }
  for (const url of [
    'https://127.0.0.1/user',
    'https://[::1]/user',
    'https://service.internal/user',
    'https://localhost/user',
    'https://example.com/',
    'https://example.com/a?token=secret',
    'https://example.com/%2e%2e/user',
    'https://example.com/%252e%252e/user',
    'https://example.com/a//b',
  ]) {
    assert.equal(validProfileURL('custom', url), false, url)
  }
})

test('snapshots preserve seconds, approximation, period and unknown metrics', () => {
  const draft = snapshot()
  draft.label = 'My Codex account'
  const result = prepareLinkedProfiles([draft], now.getTime())
  assert.equal(result.valid, true)
  assert.deepEqual(result.profiles[0]?.snapshot, {
    tokens: 117_000_000_000,
    period: 'all',
    approximate: true,
    observed_at: now.toISOString(),
    source: 'Codex lifetime tokens; owner-observed snapshot',
  })
  assert.equal(
    Object.hasOwn(result.profiles[0]?.snapshot ?? {}, 'requests'),
    false
  )
  assert.equal(
    Object.hasOwn(result.profiles[0]?.snapshot ?? {}, 'messages'),
    false
  )
})

test('zero is a known number while missing data is omitted', () => {
  const draft = snapshot()
  draft.tokens = ''
  draft.messages = '0'
  assert.deepEqual(
    prepareLinkedProfiles([draft], now.getTime()).profiles[0]?.snapshot
      ?.messages,
    0
  )
  draft.messages = ''
  assert.equal(prepareLinkedProfiles([draft], now.getTime()).valid, false)
  for (const raw of ['-1', '1.5', 'Infinity', '1e6', '9007199254740992']) {
    draft.tokens = raw
    assert.equal(
      prepareLinkedProfiles([draft], now.getTime()).valid,
      false,
      raw
    )
  }
})

test('same provider supports several accounts and rejects duplicate URLs or more than five', () => {
  const one = profileDraft(
    { provider: 'cursor', url: 'https://cursor.com/@one' },
    now
  )
  const two = profileDraft(
    { provider: 'cursor', url: 'https://cursor.com/@two' },
    now
  )
  assert.equal(prepareLinkedProfiles([one, two]).valid, true)
  assert.equal(
    prepareLinkedProfiles([one, { ...two, url: one.url }]).valid,
    false
  )
  assert.equal(
    prepareLinkedProfiles(
      Array.from({ length: 6 }, (_, i) => ({
        ...one,
        id: String(i),
        url: `https://cursor.com/@account${i}`,
      }))
    ).valid,
    false
  )
  assert.deepEqual(prepareLinkedProfiles([]), {
    profiles: [],
    errors: {},
    valid: true,
  })
})

test('snapshot observation and custom date bounds reject impossible or future periods', () => {
  const draft = snapshot()
  draft.period = 'custom'
  draft.start = '2026-09-04'
  draft.end = '2026-10-03'
  assert.equal(prepareLinkedProfiles([draft], now.getTime()).valid, true)
  for (const [start, end] of [
    ['', '2026-10-03'],
    ['2026-02-30', '2026-10-03'],
    ['2026-10-04', '2026-10-03'],
    ['2026-09-04', '2026-10-05'],
    ['2025-01-01', '2026-10-03'],
  ]) {
    assert.equal(
      prepareLinkedProfiles(
        [{ ...draft, start: start ?? '', end: end ?? '' }],
        now.getTime()
      ).valid,
      false
    )
  }
  assert.equal(
    prepareLinkedProfiles([{ ...draft, period: 'all' }], now.getTime()).valid,
    false
  )
  assert.equal(
    prepareLinkedProfiles([{ ...draft, period: '7d' }], now.getTime()).valid,
    false
  )
  assert.equal(
    prepareLinkedProfiles([{ ...draft, period: '30d' }], now.getTime()).valid,
    true
  )
  assert.equal(
    prepareLinkedProfiles(
      [{ ...snapshot(), observedAt: '2099-01-01T12:00:00' }],
      now.getTime()
    ).valid,
    false
  )
  assert.equal(
    prepareLinkedProfiles(
      [{ ...snapshot(), observedAt: '1999-01-01T12:00:00' }],
      now.getTime()
    ).valid,
    false
  )
})
