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

import { getGravatarUrl, normalizeGravatarEmail } from './avatar'

describe('Gravatar URLs', () => {
  test('normalizes email addresses before hashing', () => {
    assert.equal(
      normalizeGravatarEmail('  MyEmailAddress@example.com '),
      'myemailaddress@example.com'
    )
  })

  test('uses the documented SHA-256 avatar URL and local-fallback response', async () => {
    assert.equal(
      await getGravatarUrl(' MyEmailAddress@example.com ', 9999),
      'https://gravatar.com/avatar/84059b07d4be67b806386c0aad8070a23f18836bbaae342275dc0a83414c32ee?d=404&r=g&s=2048'
    )
    assert.equal(await getGravatarUrl('   '), null)
  })
})
