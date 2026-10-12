/*
Copyright (C) 2026 LIghtJUNction

SPDX-License-Identifier: AGPL-3.0-or-later
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import type { SystemStatus } from '../types'
import {
  GOOGLE_BUILT_IN_PRESET,
  buildGoogleBrowserStartRequest,
  isBuiltInGoogleConfigured,
  shouldShowCustomGoogleProvider,
  validateGoogleBrowserAuthorizationUrl,
} from './google-oauth'

const configured = { google_oauth: true, google_client_id: 'test-client' }
const custom = { name: 'Google', slug: 'old-google', icon: 'google' }
const origin = 'https://site.example.test'

function authorizationUrl() {
  const url = new URL(GOOGLE_BUILT_IN_PRESET.authorization_endpoint)
  for (const [key, value] of Object.entries({
    client_id: 'test-client',
    response_type: 'code',
    scope: 'openid email profile',
    state: 'synthetic-state',
    nonce: 'synthetic-nonce',
    code_challenge: 'synthetic-challenge',
    code_challenge_method: 'S256',
    redirect_uri: `${origin}${GOOGLE_BUILT_IN_PRESET.browser_callback}`,
  })) {
    url.searchParams.set(key, value)
  }
  return url
}

describe('Google browser capabilities', () => {
  test('needs explicit enabled browser support AND a public client ID', () => {
    assert.equal(isBuiltInGoogleConfigured(configured), true)
    for (const status of [
      null,
      {},
      { google_client_id: 'test-client' },
      { google_oauth: true },
      { ...configured, google_oauth: false },
      { ...configured, google_oauth: 'true' },
      { ...configured, google_client_id: ' ' },
      { ...configured, google_client_id: 123 },
      { data: configured },
      { identity: { google: { client_id: 'test-client' } } },
    ]) {
      // Deliberately malformed server JSON still has to fail the runtime guard.
      assert.equal(isBuiltInGoogleConfigured(status as unknown as SystemStatus), false)
    }
  })

  test('deduplicates only when built-in is usable; leaves legacy fallback and unrelated providers', () => {
    assert.equal(shouldShowCustomGoogleProvider(configured, custom), false)
    assert.equal(
      shouldShowCustomGoogleProvider(configured, custom, false),
      true
    )
    for (const status of [
      null,
      {},
      { ...configured, google_oauth: false },
      { google_oauth: true },
    ]) {
      assert.equal(shouldShowCustomGoogleProvider(status, custom), true)
    }
    assert.equal(
      shouldShowCustomGoogleProvider(configured, {
        name: 'GitLab',
        slug: 'gitlab',
        icon: 'gitlab',
      }),
      true
    )
  })

  test('enforces required legal consent without inventing consent when not required', () => {
    for (const key of ['user_agreement_enabled', 'privacy_policy_enabled']) {
      const status = { ...configured, [key]: true }
      assert.equal(buildGoogleBrowserStartRequest(status, false), null)
      assert.deepEqual(buildGoogleBrowserStartRequest(status, true), {
        accepted_legal: true,
      })
    }
    assert.deepEqual(buildGoogleBrowserStartRequest(configured, false), {
      accepted_legal: false,
    })
    assert.equal(buildGoogleBrowserStartRequest({}, true), null)
  })
})

describe('Google authorization response restrictions', () => {
  test('accepts only the official source and same-origin browser callback with PKCE', () => {
    const url = authorizationUrl()
    assert.equal(
      validateGoogleBrowserAuthorizationUrl(
        url.toString(),
        'test-client',
        origin
      ),
      url.toString()
    )
  })

  test('refuses native token callback, legacy SPA callback, foreign callback, fragment and credentials', () => {
    for (const callback of [
      `${origin}/core/v1/auth/oauth/google/callback`,
      `${origin}/oauth/google`,
      `https://foreign.example.test${GOOGLE_BUILT_IN_PRESET.browser_callback}`,
      `${origin}${GOOGLE_BUILT_IN_PRESET.browser_callback}?redirect=https://foreign.example.test`,
      `${origin}${GOOGLE_BUILT_IN_PRESET.browser_callback}#token`,
      `https://user:pass@site.example.test${GOOGLE_BUILT_IN_PRESET.browser_callback}`,
    ]) {
      const url = authorizationUrl()
      url.searchParams.set('redirect_uri', callback)
      assert.throws(() =>
        validateGoogleBrowserAuthorizationUrl(
          url.toString(),
          'test-client',
          origin
        )
      )
    }
  })

  test('rejects untrusted source URLs, missing proof fields, wrong client/scope/type and duplicates', () => {
    for (const mutate of [
      (url: URL) => {
        url.hostname = 'accounts.google.com.attacker.test'
      },
      (url: URL) => {
        url.protocol = 'http:'
      },
      (url: URL) => {
        url.pathname = '/other'
      },
      (url: URL) => {
        url.hash = 'token'
      },
      (url: URL) => {
        url.username = 'user'
      },
      (url: URL) => {
        url.searchParams.set('client_id', 'other-client')
      },
      (url: URL) => {
        url.searchParams.set('response_type', 'token')
      },
      (url: URL) => {
        url.searchParams.set('scope', 'openid email profile extra')
      },
      (url: URL) => {
        url.searchParams.set('code_challenge_method', 'plain')
      },
      (url: URL) => {
        url.searchParams.append('redirect_uri', 'https://attacker.test/cb')
      },
      ...['state', 'nonce', 'code_challenge'].map((key) => (url: URL) => {
        url.searchParams.delete(key)
      }),
    ]) {
      const url = authorizationUrl()
      mutate(url)
      assert.throws(() =>
        validateGoogleBrowserAuthorizationUrl(
          url.toString(),
          'test-client',
          origin
        )
      )
    }
    assert.throws(() =>
      validateGoogleBrowserAuthorizationUrl(
        { access_token: 'not-browser-login' },
        'test-client',
        origin
      )
    )
  })

  test('permits loopback HTTP only explicitly in local development', () => {
    const local = 'http://127.0.0.1:4174'
    const url = authorizationUrl()
    url.searchParams.set(
      'redirect_uri',
      `${local}${GOOGLE_BUILT_IN_PRESET.browser_callback}`
    )
    assert.throws(() =>
      validateGoogleBrowserAuthorizationUrl(
        url.toString(),
        'test-client',
        local
      )
    )
    assert.equal(
      validateGoogleBrowserAuthorizationUrl(
        url.toString(),
        'test-client',
        local,
        true
      ),
      url.toString()
    )
  })
})
