/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { discoverOIDCSettings, OIDCDiscoveryError } from './oidc-discovery'

const configuration = (
  enabled = true,
  wellKnown = 'https://id.example/config'
) => ({
  enabled,
  well_known: wellKnown,
  authorization_endpoint: 'https://id.example/auth',
  token_endpoint: 'https://id.example/token',
  user_info_endpoint: 'https://id.example/user',
  client_secret: 'fixture',
})

const unexpectedFetch = async () => {
  assert.fail('discovery must not run for this save')
}

for (const enabled of [true, false]) {
  test(`unchanged OIDC does not block unrelated or unchanged saves (enabled=${enabled})`, async () => {
    const values = configuration(enabled)
    const result = await discoverOIDCSettings(values, values, unexpectedFetch)
    assert.equal(result.oidc, values)
    assert.equal(result.discovered, false)
  })
}

test('whitespace-only differences do not trigger discovery', async () => {
  const saved = configuration(true, ' https://id.example/config ')
  const values = configuration(true, '\thttps://id.example/config\n')
  const result = await discoverOIDCSettings(values, saved, unexpectedFetch)
  assert.equal(result.discovered, false)
})

test('disabling works with an invalid URL, even when the URL also changed', async () => {
  for (const url of ['not a URL', 'https://offline.example/config']) {
    const saved = configuration(true, url)
    for (const next of [url, 'another invalid URL']) {
      const values = configuration(false, next)
      const result = await discoverOIDCSettings(values, saved, unexpectedFetch)
      assert.equal(result.oidc, values)
      assert.equal(result.discovered, false)
    }
  }
})

test('empty discovery URL preserves manual endpoint configuration', async () => {
  const values = configuration(true, '')
  const result = await discoverOIDCSettings(
    values,
    configuration(false, ''),
    unexpectedFetch
  )
  assert.equal(result.oidc, values)
})

for (const mode of ['enable', 'change URL', 'change URL while disabled']) {
  test(`${mode} discovers once and returns trimmed settings without mutating input`, async () => {
    const enabled = mode !== 'change URL while disabled'
    const values = configuration(enabled, ' https://new.example/config ')
    const saved = configuration(
      mode === 'change URL',
      mode === 'enable' ? values.well_known : 'https://old.example/config'
    )
    const before = { ...values }
    const calls: string[] = []
    const result = await discoverOIDCSettings(values, saved, async (url) => {
      calls.push(url)
      return {
        authorization_endpoint: 'https://new.example/auth',
        token_endpoint: 'https://new.example/token',
      }
    })
    assert.deepEqual(calls, ['https://new.example/config'])
    assert.equal(result.discovered, true)
    assert.equal(result.oidc.well_known, 'https://new.example/config')
    assert.equal(result.oidc.authorization_endpoint, 'https://new.example/auth')
    assert.equal(result.oidc.token_endpoint, 'https://new.example/token')
    assert.equal(result.oidc.user_info_endpoint, '')
    assert.equal(result.oidc.client_secret, 'fixture')
    assert.deepEqual(values, before)
  })
}

test('invalid changed URLs fail before any request', async () => {
  for (const url of [
    'not a URL',
    'http://',
    'file:///tmp/config',
    'javascript:void(0)',
  ]) {
    await assert.rejects(
      discoverOIDCSettings(
        configuration(true, url),
        configuration(),
        unexpectedFetch
      ),
      (error: unknown) =>
        error instanceof OIDCDiscoveryError && error.kind === 'url'
    )
  }
})

test('failed discovery cannot hand partial settings to persistence or leak request data', async () => {
  const values = configuration(true, 'https://new.example/config')
  const before = { ...values }
  let persisted = false
  await assert.rejects(
    async () => {
      await discoverOIDCSettings(values, configuration(), async () => {
        throw new Error('private provider response')
      })
      persisted = true
    },
    (error: unknown) =>
      error instanceof OIDCDiscoveryError &&
      error.kind === 'fetch' &&
      !error.message.includes('private')
  )
  assert.equal(persisted, false)
  assert.deepEqual(values, before)
})

test('submission remains awaiting discovery until the pending request resolves', async () => {
  let resolve!: (value: unknown) => void
  const pending = new Promise<unknown>((done) => {
    resolve = done
  })
  let finished = false
  const result = discoverOIDCSettings(
    configuration(true),
    configuration(false),
    () => pending
  ).then((value) => {
    finished = true
    return value
  })
  await Promise.resolve()
  assert.equal(finished, false)
  resolve({})
  assert.equal((await result).discovered, true)
  assert.equal(finished, true)
})

test('malformed discovery documents are rejected without changing the form values', async () => {
  for (const document of [null, [], 'html', { token_endpoint: {} }]) {
    const values = configuration(true)
    const before = { ...values }
    await assert.rejects(
      discoverOIDCSettings(
        values,
        configuration(false),
        async () => document
      ),
      (error: unknown) =>
        error instanceof OIDCDiscoveryError && error.kind === 'fetch'
    )
    assert.deepEqual(values, before)
  }
})
