/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { RefreshOutcome } from '@/lib/auth-session'

import {
  AuthBootstrapPendingError,
  waitForProtectedAuthentication,
} from './auth-bootstrap-navigation'

function deferred() {
  let resolve!: (value: RefreshOutcome) => void
  const promise = new Promise<RefreshOutcome>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

test('a queued session check stays pending until its actual terminal outcome', async () => {
  const task = deferred()
  let completed = false
  const navigation = waitForProtectedAuthentication(
    task.promise,
    '/profile'
  ).then(() => {
    completed = true
  })
  await new Promise((resolve) => setTimeout(resolve, 20))
  assert.equal(completed, false)
  task.resolve({ kind: 'anonymous' })
  await navigation
  assert.equal(completed, true)
})

test('a navigation budget exposes the same live task without cancelling it', async () => {
  const task = deferred()
  let pending: unknown
  try {
    await waitForProtectedAuthentication(task.promise, '/profile', undefined, 5)
  } catch (error) {
    pending = error
  }
  assert.ok(pending instanceof AuthBootstrapPendingError)
  assert.strictEqual(pending.task, task.promise)
  assert.equal(pending.href, '/profile')
  task.resolve({ kind: 'anonymous' })
  assert.deepEqual(await pending.task, { kind: 'anonymous' })
})

test('navigation abort only ends its wait while the existing authentication task completes', async () => {
  const task = deferred(),
    controller = new AbortController()
  const navigation = waitForProtectedAuthentication(
    task.promise,
    '/profile',
    controller.signal
  )
  controller.abort()
  await assert.rejects(navigation, { name: 'AbortError' })
  task.resolve({ kind: 'anonymous' })
  assert.deepEqual(await task.promise, { kind: 'anonymous' })
})

test('already cancelled navigation does not wait or change the task', async () => {
  const task = deferred(),
    controller = new AbortController()
  controller.abort()
  await assert.rejects(
    waitForProtectedAuthentication(task.promise, '/profile', controller.signal),
    { name: 'AbortError' }
  )
  task.resolve({ kind: 'anonymous' })
})
