/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  getTrustedTemplatedUrl,
  getTrustedUrlFromSource,
  validatedExternalUrl,
} from './validated-external-url'

const consolePolicy = {
  protocols: ['https:'],
  origins: ['https://console.example.test'],
  hosts: ['console.example.test'],
  paths: { exact: ['/models/deployments'] },
} as const

describe('validatedExternalUrl', () => {
  test('accepts only the configured same-origin route', () => {
    assert.equal(
      validatedExternalUrl(
        '/models/deployments?dFilter=42',
        consolePolicy,
        'https://console.example.test'
      ),
      'https://console.example.test/models/deployments?dFilter=42'
    )

    for (const unsafe of [
      'javascript:alert(1)',
      'https://user@console.example.test/models/deployments',
      'https://evil.example/models/deployments',
      'https://console.example.test/models',
      'https://console.example.test/models/deployments#secret',
      ' /models/deployments',
    ]) {
      assert.equal(
        validatedExternalUrl(
          unsafe,
          consolePolicy,
          'https://console.example.test'
        ),
        null,
        unsafe
      )
    }
  })

  test('enforces segment boundaries for prefix policies', () => {
    const policy = {
      ...consolePolicy,
      paths: { prefixes: ['/docs'] },
    } as const

    assert.equal(
      validatedExternalUrl('https://console.example.test/docs/api', policy),
      'https://console.example.test/docs/api'
    )
    assert.equal(
      validatedExternalUrl('https://console.example.test/docstring', policy),
      null
    )
  })
})

describe('trusted URL comparisons', () => {
  test('requires source and target to share protocol, origin, host, and path', () => {
    const source = 'https://id.example/oauth/callback?provider=github'
    assert.equal(
      getTrustedUrlFromSource(
        'https://id.example/oauth/callback?code=abc',
        source,
        ['https:']
      ),
      'https://id.example/oauth/callback?code=abc'
    )
    assert.equal(
      getTrustedUrlFromSource(
        'https://evil.example/oauth/callback?code=abc',
        source,
        ['https:']
      ),
      null
    )
  })

  test('matches only escaped template paths on the trusted origin', () => {
    const template = 'https://chat.example/session/%7Bkey%7D'
    assert.equal(
      getTrustedTemplatedUrl(
        'https://chat.example/session/sk-safe',
        template,
        ['https:']
      ),
      'https://chat.example/session/sk-safe'
    )
    assert.equal(
      getTrustedTemplatedUrl(
        'https://chat.example/session/a/b',
        template,
        ['https:']
      ),
      null
    )
    assert.equal(
      getTrustedTemplatedUrl(
        'https://evil.example/session/sk-safe',
        template,
        ['https:']
      ),
      null
    )
  })
})
