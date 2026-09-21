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
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  checkVerificationMethods,
  type VerificationProbeDependencies,
} from './api'

function createDependencies(
  overrides: Partial<VerificationProbeDependencies> = {}
): VerificationProbeDependencies {
  return {
    getSelf: async () => ({
      success: true,
      data: { email: 'owner@example.com' },
    }),
    get2FAStatus: async () => ({
      success: true,
      data: { enabled: true },
    }),
    getPasskeyStatus: async () => ({
      success: true,
      data: { enabled: true },
    }),
    detectPasskeySupport: async () => true,
    wait: async () => undefined,
    warn: () => undefined,
    ...overrides,
  }
}

describe('secure verification method probes', () => {
  test('reports a complete result when every server probe succeeds', async () => {
    const result = await checkVerificationMethods(createDependencies())

    assert.equal(result.availability, 'complete')
    assert.equal(result.hasEmail, true)
    assert.equal(result.has2FA, true)
    assert.equal(result.hasPasskey, true)
    assert.equal(result.passkeySupported, true)
  })

  test('retries a transient probe once without hiding other methods', async () => {
    let accountCalls = 0
    const waits: number[] = []
    const warnings: string[] = []
    const result = await checkVerificationMethods(
      createDependencies({
        getSelf: async () => {
          accountCalls += 1
          if (accountCalls === 1) throw new Error('network down')
          return { success: true, data: { email: 'owner@example.com' } }
        },
        wait: async (delay) => {
          waits.push(delay)
        },
        warn: (message) => {
          warnings.push(message)
        },
      })
    )

    assert.equal(result.availability, 'complete')
    assert.equal(result.hasEmail, true)
    assert.equal(accountCalls, 2)
    assert.deepEqual(waits, [250])
    assert.deepEqual(warnings, [])
  })

  test('marks a non-retryable single-probe failure as partial', async () => {
    const waits: number[] = []
    const warnings: string[] = []
    const unauthorized = Object.assign(new Error('unauthorized'), {
      response: { status: 401 },
    })
    const result = await checkVerificationMethods(
      createDependencies({
        getSelf: async () => {
          throw unauthorized
        },
        wait: async (delay) => {
          waits.push(delay)
        },
        warn: (message) => {
          warnings.push(message)
        },
      })
    )

    assert.equal(result.availability, 'partial')
    assert.equal(result.hasEmail, false)
    assert.equal(result.has2FA, true)
    assert.equal(result.hasPasskey, true)
    assert.deepEqual(waits, [])
    assert.deepEqual(warnings, [
      '[Secure Verification] Failed to check account',
    ])
  })

  test('distinguishes an unavailable check from a confirmed empty result', async () => {
    const unauthorized = Object.assign(new Error('unauthorized'), {
      response: { status: 401 },
    })
    const fail = async (): Promise<never> => {
      throw unauthorized
    }
    const result = await checkVerificationMethods(
      createDependencies({
        getSelf: fail,
        get2FAStatus: fail,
        getPasskeyStatus: fail,
      })
    )

    assert.equal(result.availability, 'unavailable')
    assert.equal(result.hasEmail, false)
    assert.equal(result.has2FA, false)
    assert.equal(result.hasPasskey, false)
  })
})
