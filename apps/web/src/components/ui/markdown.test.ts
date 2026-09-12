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
import { afterEach, beforeEach, describe, test } from 'node:test'

import { isExternalUrl } from './markdown'

describe('markdown isExternalUrl classification', () => {
  const originalWindow = globalThis.window

  beforeEach(() => {
    Object.defineProperty(globalThis, 'window', {
      value: {
        location: {
          href: 'https://api.lmm.best/wallet?tab=recharge',
          origin: 'https://api.lmm.best',
        },
      },
      writable: true,
      configurable: true,
    })
  })

  afterEach(() => {
    Object.defineProperty(globalThis, 'window', {
      value: originalWindow,
      writable: true,
      configurable: true,
    })
  })

  test('classifies protocol-relative URLs as external', () => {
    assert.equal(isExternalUrl('//outside.example/help'), true)
    assert.equal(isExternalUrl('//github.com/org/repo'), true)
    assert.equal(isExternalUrl('  //external-domain.com/path  '), true)
  })

  test('classifies absolute HTTP and HTTPS URLs from different origins as external', () => {
    assert.equal(isExternalUrl('https://example.com'), true)
    assert.equal(isExternalUrl('http://insecure.example.com/page'), true)
    assert.equal(isExternalUrl('https://other.lmm.best/docs'), true)
  })

  test('classifies same-origin absolute URLs as internal', () => {
    assert.equal(isExternalUrl('https://api.lmm.best/wallet'), false)
    assert.equal(isExternalUrl('https://api.lmm.best/terms#service'), false)
    assert.equal(
      isExternalUrl('https://api.lmm.best?discount_code=SPECIAL'),
      false
    )
  })

  test('classifies relative path URLs as internal', () => {
    assert.equal(isExternalUrl('/wallet'), false)
    assert.equal(isExternalUrl('/pricing'), false)
    assert.equal(isExternalUrl('./relative-page'), false)
    assert.equal(isExternalUrl('relative-subpage'), false)
  })

  test('classifies anchor and query links as internal', () => {
    assert.equal(isExternalUrl('#section-1'), false)
    assert.equal(isExternalUrl('?param=value'), false)
  })

  test('classifies special protocols (mailto, tel, javascript) as non-external', () => {
    assert.equal(isExternalUrl('mailto:support@example.com'), false)
    assert.equal(isExternalUrl('tel:+1234567890'), false)
    assert.equal(isExternalUrl('javascript:void(0)'), false)
  })

  test('returns false for empty or nullish href', () => {
    assert.equal(isExternalUrl(''), false)
    assert.equal(isExternalUrl('   '), false)
  })
})
