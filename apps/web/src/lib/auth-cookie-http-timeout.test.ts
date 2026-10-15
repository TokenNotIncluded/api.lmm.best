/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { after, test } from 'node:test'

import axios from 'axios'

import { login, logout } from '@/features/auth/api'

import { api } from './api'
import {
  AUTH_COOKIE_TIMEOUT_MS,
  AuthCookieBlockedError,
  getAuthCookieBlockedState,
  withAuthCookieLock,
} from './auth-cookie'
import { clearAuthentication } from './auth-session'

const descriptors = new Map(
  ['window', 'navigator', 'isSecureContext'].map((key) => [
    key,
    Object.getOwnPropertyDescriptor(globalThis, key),
  ])
)
const storage = new Map<string, string>()
let held = 0,
  tail: Promise<void> = Promise.resolve()
Object.defineProperty(globalThis, 'isSecureContext', {
  configurable: true,
  value: true,
})
Object.defineProperty(globalThis, 'navigator', {
  configurable: true,
  value: {
    locks: {
      async request<T>(
        _name: string,
        _options: unknown,
        callback: () => Promise<T>
      ) {
        const previous = tail
        let release!: () => void
        tail = new Promise<void>((done) => {
          release = done
        })
        await previous
        held++
        try {
          return await callback()
        } finally {
          held--
          release()
        }
      },
    },
  },
})
Object.defineProperty(globalThis, 'window', {
  configurable: true,
  value: {
    localStorage: {
      getItem: (key: string) => storage.get(key) ?? null,
      setItem: (key: string, value: string) => storage.set(key, value),
      removeItem: (key: string) => storage.delete(key),
    },
  },
})
const originalAdapter = api.defaults.adapter,
  originalBase = api.defaults.baseURL
after(() => {
  api.defaults.adapter = originalAdapter
  api.defaults.baseURL = originalBase
  clearAuthentication(false, 'idle')
  for (const [key, descriptor] of descriptors) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor)
    else Reflect.deleteProperty(globalThis, key)
  }
})

test('a real stalled HTTP response times out, releases the lock into blocked recovery and never dispatches a successor', async () => {
  let requests = 0,
    entered!: () => void
  const started = new Promise<void>((done) => {
    entered = done
  })
  const server = createServer((_request, _response) => {
    requests++
    entered()
  })
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  const address = server.address()
  assert.ok(address && typeof address === 'object')
  api.defaults.baseURL = `http://127.0.0.1:${address.port}`
  api.defaults.adapter = axios.getAdapter('http')
  clearAuthentication(false, 'idle')
  const controller = new AbortController()
  const startedAt = Date.now()
  const failed = assert.rejects(
    login(
      { username: 'synthetic', password: 'synthetic-password' },
      controller.signal
    ),
    AuthCookieBlockedError
  )
  try {
    await started
    controller.abort()
    let successorRan = false
    const successor = assert.rejects(
      withAuthCookieLock(async () => {
        successorRan = true
      }),
      AuthCookieBlockedError
    )
    assert.equal(held, 1)
    assert.equal(getAuthCookieBlockedState(), null)
    assert.ok(
      [...storage.values()].some((value) => value.startsWith('pending:'))
    )
    await Promise.all([failed, successor])
    const elapsed = Date.now() - startedAt
    assert.ok(elapsed >= AUTH_COOKIE_TIMEOUT_MS - 500)
    assert.ok(elapsed < AUTH_COOKIE_TIMEOUT_MS + 5_000)
    assert.equal(held, 0)
    assert.equal(successorRan, false)
    assert.equal(getAuthCookieBlockedState(), 'uncertain')
    assert.ok(
      [...storage.values()].some((value) => value.startsWith('uncertain:'))
    )
    await assert.rejects(logout(), AuthCookieBlockedError)
    assert.equal(requests, 1)
  } finally {
    server.closeAllConnections()
    await new Promise<void>((resolve, reject) =>
      server.close((error) => {
        // Bun's closeAllConnections may already close the listening server.
        if (
          error &&
          (error as NodeJS.ErrnoException).code !== 'ERR_SERVER_NOT_RUNNING'
        ) {
          reject(error)
        } else resolve()
      })
    )
  }
})
