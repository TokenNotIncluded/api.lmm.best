/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { fetchRepositoryStars } from './api'

test('repository stars are read without account credentials', async () => {
  const request = (async (url: string, init: RequestInit) => {
    assert.equal(
      url,
      'https://api.github.com/repos/TokenNotIncluded/lmm-scripts'
    )
    assert.equal(init.credentials, 'omit')
    assert.equal(new Headers(init.headers).has('Authorization'), false)
    return new Response(
      JSON.stringify({
        full_name: 'TokenNotIncluded/lmm-scripts',
        stargazers_count: 27,
      })
    )
  }) as typeof fetch
  assert.equal(await fetchRepositoryStars('scripts', undefined, request), 27)
})
test('failed or malformed statistics never become a fabricated zero', async () => {
  for (const response of [
    new Response('{}', { status: 429 }),
    new Response(
      JSON.stringify({ full_name: 'other/repo', stargazers_count: 0 })
    ),
    new Response(
      JSON.stringify({
        full_name: 'TokenNotIncluded/lmm-scripts',
        stargazers_count: -1,
      })
    ),
  ]) {
    await assert.rejects(
      fetchRepositoryStars(
        'scripts',
        undefined,
        (async () => response) as typeof fetch
      )
    )
  }
})
