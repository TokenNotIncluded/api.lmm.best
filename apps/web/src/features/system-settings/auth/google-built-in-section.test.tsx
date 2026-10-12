/*
Copyright (C) 2026 LIghtJUNction

SPDX-License-Identifier: AGPL-3.0-or-later
*/
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock } from 'bun:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { describe, test } from 'node:test'

import { renderToStaticMarkup } from 'react-dom/server'

mock.module('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}))
const { GoogleBuiltInSection } = await import('./google-built-in-section')

describe('Google built-in configuration guide', () => {
  test('shows fixed official endpoints/scopes and browser callback, never native raw-token callback', () => {
    const html = renderToStaticMarkup(
      <GoogleBuiltInSection serverAddress='https://site.example.test/' />
    )
    assert.match(
      html,
      /https:\/\/site.example.test\/api\/oauth\/google\/callback/
    )
    assert.match(html, /https:\/\/accounts.google.com\/o\/oauth2\/v2\/auth/)
    assert.match(html, /https:\/\/oauth2.googleapis.com\/token/)
    assert.match(html, /https:\/\/openidconnect.googleapis.com\/v1\/userinfo/)
    assert.match(html, /openid profile email/)
    assert.doesNotMatch(html, /core\/v1\/auth\/oauth|\/oauth\/google&quot;/)
  })

  test('explicit read-only TOML credentials do not advertise supported web saving', () => {
    const html = renderToStaticMarkup(<GoogleBuiltInSection serverAddress='' />)
    assert.match(html, /Read-only/)
    assert.match(html, /Configuration File/)
    assert.match(html, /\[identity.google\]/)
    assert.match(html, /client_secret_file/)
    const inputs = html.match(/<input\b[^>]*>/g) ?? []
    assert.equal(inputs.length, 3)
    assert.ok(
      inputs.every((input) => /readonly=""/i.test(input)),
      inputs.join('\n')
    )
  })

  test('the tab disables saving and the submit path refuses legacy option writes for Google', () => {
    // A source wiring assertion, not a browser/service acceptance claim.
    const source = readFileSync(
      new URL('./oauth-section.tsx', import.meta.url),
      'utf8'
    )
    assert.match(source, /isSaveDisabled=\{activeTab === 'google'\}/)
    assert.match(source, /if \(activeTab === 'google'\) return/)
    assert.match(source, /<TabsTrigger value='google'>Google<\/TabsTrigger>/)
    assert.match(
      source,
      /<GoogleBuiltInSection serverAddress=\{props.serverAddress\} \/>/
    )
  })
})
