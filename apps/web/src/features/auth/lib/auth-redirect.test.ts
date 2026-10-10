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
import { describe, test } from 'node:test'

import type { AuthUser } from '@/stores/auth-store'

import { getSavedLanguage, sanitizeAuthRedirect, signInHref } from './auth-redirect'

const origin = 'https://dashboard.example.com'

describe('authentication redirect validation', () => {
  test('preserves safe internal paths, search parameters, and fragments', () => {
    assert.equal(
      sanitizeAuthRedirect('/console?tab=usage#recent', origin),
      '/console?tab=usage#recent'
    )
    assert.equal(
      sanitizeAuthRedirect(
        'https://dashboard.example.com/dashboard?tab=quota#daily',
        origin
      ),
      '/dashboard?tab=quota#daily'
    )
  })

  test('preserves a product SKU, quantity, encoded promotion and fragment', () => {
    const productPath =
      '/store/products/product-fixture?variant_id=variant-fixture&quantity=3&promotion=SUMMER%2B10#purchase'
    assert.equal(sanitizeAuthRedirect(productPath, origin), productPath)
    assert.equal(
      sanitizeAuthRedirect(`${origin}${productPath}`, origin),
      productPath
    )
  })

  test('rejects external and ambiguously parsed redirect targets', () => {
    const unsafeTargets: unknown[] = [
      undefined,
      '',
      'dashboard',
      '//attacker.example/path',
      'https://attacker.example/path',
      'javascript:alert(1)',
      '/\\attacker.example/path',
      'https:\\attacker.example/path',
    ]

    for (const target of unsafeTargets) {
      assert.equal(sanitizeAuthRedirect(target, origin), null)
    }
  })

  test('rejects invalid or non-HTTP application origins', () => {
    assert.equal(sanitizeAuthRedirect('/dashboard', 'not-an-origin'), null)
    assert.equal(sanitizeAuthRedirect('/dashboard', 'file:///tmp/app'), null)
  })
})

describe('saved authentication language', () => {
  const user: AuthUser = { id: 1, username: 'user', role: 1 }

  test('prefers the explicit user language', () => {
    assert.equal(
      getSavedLanguage({
        ...user,
        language: 'ja',
        setting: { language: 'fr' },
      }),
      'ja'
    )
  })

  test('reads object and JSON string settings', () => {
    assert.equal(
      getSavedLanguage({ ...user, setting: { language: 'fr' } }),
      'fr'
    )
    assert.equal(
      getSavedLanguage({ ...user, setting: '{"language":"ru"}' }),
      'ru'
    )
  })

  test('ignores malformed and non-string setting languages', () => {
    assert.equal(getSavedLanguage({ ...user, setting: '{' }), undefined)
    assert.equal(
      getSavedLanguage({ ...user, setting: { language: 123 } }),
      undefined
    )
  })
})

describe('store pickup login returns', () => {
  test('preserves the complete internal pickup URL and explicitly permits account correction', () => {
    const location = { origin, pathname: '/store/claim/' + 'x'.repeat(43), search: '?source=mail%2Breceipt', hash: '#refunds' }
    const href = new URL(signInHref(location, true), origin)
    assert.equal(href.pathname, '/sign-in')
    assert.equal(href.searchParams.get('redirect'), location.pathname + location.search + location.hash)
    assert.equal(href.searchParams.get('reauth'), '1')
    assert.equal(new URL(signInHref(location), origin).searchParams.has('reauth'), false)
  })
  test('does not accept an external return or a login loop', () => {
    assert.equal(signInHref({ origin, pathname: '//attacker.example', search: '', hash: '' }), '/sign-in')
    assert.equal(signInHref({ origin, pathname: '/sign-in', search: '?redirect=/dashboard', hash: '' }), '/sign-in')
  })
})
