/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { sourceEntry } from './source-entry'

test('source capture strips credentials, fragments and unrelated query fields before sending', () => {
  const entry = sourceEntry(
    'https://api.lmm.best/guide?utm_source=github&utm_campaign=readme&api_key=SECRET#PRIVATE',
    'https://github.com/org/repo?token=SECRET'
  )
  assert.ok(entry)
  assert.equal(entry.referrer, 'https://github.com')
  assert.equal(entry.landing, '/guide')
  assert.equal(entry.source, 'github')
  assert.equal(JSON.stringify(entry).includes('SECRET'), false)
  assert.equal(JSON.stringify(entry).includes('PRIVATE'), false)
  assert.equal(
    sourceEntry(
      'https://api.lmm.best/oauth/github?code=SECRET',
      'https://github.com'
    ),
    null
  )
  assert.equal(sourceEntry('https://api.lmm.best/?source_test=1', ''), null)
  assert.equal(
    sourceEntry('https://api.lmm.best/?utm_source=sk-secret', '')?.source,
    ''
  )
  assert.equal(
    sourceEntry('https://api.lmm.best/?utm_source=eyJhbGciOi.test.secret', '')
      ?.source,
    ''
  )
  assert.equal(
    sourceEntry(
      'https://api.lmm.best/wallet?success=true',
      'https://checkout.stripe.com'
    ),
    null
  )
  assert.equal(
    sourceEntry(
      'https://api.lmm.best/sign-in?code=private',
      'https://github.com'
    ),
    null
  )
  assert.equal(
    sourceEntry('https://api.lmm.best/', 'http://192.168.1.1/private')
      ?.referrer,
    ''
  )
})

test('unsupported privacy APIs and invalid URLs cannot break site rendering', () => {
  assert.equal(sourceEntry('not a URL', ''), null)
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'crypto')
  Object.defineProperty(globalThis, 'crypto', {
    configurable: true,
    value: undefined,
  })
  try {
    assert.equal(sourceEntry('https://api.lmm.best/', ''), null)
  } finally {
    if (descriptor) Object.defineProperty(globalThis, 'crypto', descriptor)
    else Reflect.deleteProperty(globalThis, 'crypto')
  }
})
