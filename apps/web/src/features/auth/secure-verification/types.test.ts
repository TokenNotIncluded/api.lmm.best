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
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { getPreferredVerificationMethods } from './types'

describe('preferred secure verification methods', () => {
  test('uses bound email and hides step-up alternatives', () => {
    const result = getPreferredVerificationMethods({
      hasEmail: true,
      emailHint: 'o***r@example.com',
      has2FA: true,
      hasPasskey: true,
      passkeySupported: true,
      availability: 'complete',
    })

    assert.deepEqual(result, {
      hasEmail: true,
      emailHint: 'o***r@example.com',
      has2FA: false,
      hasPasskey: false,
      passkeySupported: true,
      availability: 'complete',
    })
  })

  test('falls back to 2FA when no email is bound', () => {
    const result = getPreferredVerificationMethods({
      hasEmail: false,
      has2FA: true,
      hasPasskey: true,
      passkeySupported: true,
      availability: 'complete',
    })

    assert.equal(result.hasEmail, false)
    assert.equal(result.has2FA, true)
    assert.equal(result.hasPasskey, false)
  })

  test('falls back to a supported Passkey when email and 2FA are unavailable', () => {
    const result = getPreferredVerificationMethods({
      hasEmail: false,
      has2FA: false,
      hasPasskey: true,
      passkeySupported: true,
      availability: 'complete',
    })

    assert.equal(result.hasEmail, false)
    assert.equal(result.has2FA, false)
    assert.equal(result.hasPasskey, true)
  })

  test('fails closed when no proof method is usable', () => {
    const result = getPreferredVerificationMethods({
      hasEmail: false,
      has2FA: false,
      hasPasskey: true,
      passkeySupported: false,
      availability: 'complete',
    })

    assert.equal(result.hasEmail, false)
    assert.equal(result.has2FA, false)
    assert.equal(result.hasPasskey, false)
  })
})
