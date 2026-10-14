/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import {
  AuthCookieBlockedError,
  getAuthCookieBlockedState,
  withAuthCookieLock,
} from './auth-cookie'

const descriptors = new Map(
  ['window', 'navigator', 'isSecureContext'].map((key) => [
    key,
    Object.getOwnPropertyDescriptor(globalThis, key),
  ])
)
const marker = 'pending:synthetic-crashed-writer'
Object.defineProperty(globalThis, 'isSecureContext', {
  configurable: true,
  value: true,
})
Object.defineProperty(globalThis, 'navigator', {
  configurable: true,
  value: {
    locks: {
      request: async (
        _name: string,
        _options: unknown,
        callback: () => Promise<unknown>
      ) => callback(),
    },
  },
})
Object.defineProperty(globalThis, 'window', {
  configurable: true,
  value: {
    localStorage: {
      getItem: (key: string) =>
        key === 'new-api:auth-cookie:uncertain' ? marker : null,
      setItem: () => {
        throw new Error('Existing marker must not be overwritten')
      },
    },
  },
})
after(() => {
  for (const [key, descriptor] of descriptors) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor)
    else Reflect.deleteProperty(globalThis, key)
  }
})
test('a fresh document with a crashed writer intent blocks before dispatch', async () => {
  let attempts = 0
  await assert.rejects(
    withAuthCookieLock(async (transaction) =>
      transaction.request(async () => {
        attempts++
      })
    ),
    AuthCookieBlockedError
  )
  assert.equal(getAuthCookieBlockedState(), 'uncertain')
  assert.equal(attempts, 0)
})
