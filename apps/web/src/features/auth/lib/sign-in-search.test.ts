/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { defaultParseSearch } from '@tanstack/react-router'

import { signInHref } from './auth-redirect'
import { signInSearchSchema } from './sign-in-search'

test('the real router parser accepts a generated purchasing-account sign-in link', () => {
  const location = {
    origin: 'https://store.example',
    pathname: '/store/claim/' + 'a'.repeat(43),
    search: '?from=receipt',
    hash: '#refunds',
  }
  const href = new URL(signInHref(location, true), location.origin)
  const parsed = defaultParseSearch(href.search)
  assert.equal(parsed.reauth, 1)
  assert.deepEqual(signInSearchSchema.parse(parsed), {
    redirect: location.pathname + location.search + location.hash,
    reauth: '1',
  })
})

test('a router-serialized string marker also preserves explicit reauthentication', () => {
  assert.equal(
    signInSearchSchema.parse(defaultParseSearch('?reauth=%221%22')).reauth,
    '1'
  )
})

test('absent or malformed markers never enable reauthentication or break sign-in', () => {
  for (const value of [undefined, null, 0, 2, true, false, 'true', ['1'], {}]) {
    assert.equal(signInSearchSchema.parse({ reauth: value }).reauth, undefined)
  }
  assert.deepEqual(signInSearchSchema.parse({}), {})
})

test('non-string destinations are ignored before the same-origin redirect check', () => {
  assert.equal(
    signInSearchSchema.parse({ redirect: ['//outside.example'] }).redirect,
    undefined
  )
})
