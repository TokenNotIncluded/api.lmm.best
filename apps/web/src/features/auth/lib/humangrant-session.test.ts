/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { readHumanGrantSettings, writeHumanGrantSettings } from './humangrant'
import {
  createHumanGrantSessionScope,
  type HumanGrantIdentity,
} from './humangrant-session'

const first = { userId: 1, sessionId: 'synthetic-session-a' }
const second = { userId: 2, sessionId: 'synthetic-session-b' }
const settings = { enabled: false, csrf: 'synthetic-proof' }
const signal = () => new AbortController().signal

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

test('same-session requests retain raw cookie and CSRF behavior', async () => {
  const methods: string[] = []
  const fetcher: typeof fetch = async (_url, init) => {
    methods.push(init?.method ?? 'GET')
    assert.equal(init?.credentials, 'same-origin')
    assert.equal(new Headers(init?.headers).has('Authorization'), false)
    return Response.json(init?.method === 'PUT' ? { enabled: false } : settings)
  }
  const scope = createHumanGrantSessionScope(first, () => first, fetcher)
  assert.deepEqual(
    await writeHumanGrantSettings(settings, signal(), scope.fetch),
    settings
  )
  assert.deepEqual(methods, ['PUT', 'GET'])
})

test('logout, another account or a new session refuses before any request', async () => {
  let calls = 0
  const fetcher: typeof fetch = async () => {
    calls++
    return Response.json(settings)
  }
  const cases: HumanGrantIdentity[] = [
    { userId: undefined, sessionId: undefined },
    second,
    { ...first, sessionId: 'synthetic-session-new' },
  ]
  for (const current of cases) {
    const scope = createHumanGrantSessionScope(first, () => current, fetcher)
    await assert.rejects(
      writeHumanGrantSettings(settings, signal(), scope.fetch),
      { name: 'AbortError' }
    )
  }
  assert.equal(calls, 0)
})

test('an anonymous or malformed local identity cannot acquire a page proof', async () => {
  let calls = 0
  const fetcher: typeof fetch = async () => {
    calls++
    return Response.json(settings)
  }
  for (const identity of [
    { userId: undefined, sessionId: undefined },
    { userId: 0, sessionId: 'synthetic' },
    { userId: 1, sessionId: '' },
    { userId: Number.NaN, sessionId: 'synthetic' },
  ]) {
    const scope = createHumanGrantSessionScope(
      identity,
      () => identity,
      fetcher
    )
    await assert.rejects(readHumanGrantSettings(signal(), scope.fetch), {
      name: 'AbortError',
    })
  }
  assert.equal(calls, 0)
})

test('late PUT acknowledgement cannot read settings under a different session', async () => {
  let identity = first
  let calls = 0
  let cancelled = 0
  const pending = deferred<Response>()
  const fetcher: typeof fetch = async () => {
    calls++
    return pending.promise
  }
  const scope = createHumanGrantSessionScope(first, () => identity, fetcher)
  const work = writeHumanGrantSettings(settings, signal(), scope.fetch)
  identity = second
  const body = new ReadableStream<Uint8Array>({
    cancel() {
      cancelled++
    },
  })
  pending.resolve(
    new Response(body, { headers: { 'content-type': 'application/json' } })
  )
  await assert.rejects(work, { name: 'AbortError' })
  assert.equal(calls, 1)
  assert.equal(cancelled, 1)
})

test('session change while parsing an acknowledgement stops readback', async () => {
  let identity = first
  let calls = 0
  const parsing = deferred<void>()
  const body = deferred<unknown>()
  const fetcher: typeof fetch = async () => {
    calls++
    const response = Response.json({ enabled: false })
    response.json = () => {
      parsing.resolve()
      return body.promise
    }
    return response
  }
  const scope = createHumanGrantSessionScope(first, () => identity, fetcher)
  const work = writeHumanGrantSettings(settings, signal(), scope.fetch)
  await parsing.promise
  identity = second
  body.resolve({ enabled: false })
  await assert.rejects(work, { name: 'AbortError' })
  assert.equal(calls, 1)
})

test('session change while parsing settings refuses stale account data', async () => {
  let identity = first
  let calls = 0
  const parsing = deferred<void>()
  const body = deferred<unknown>()
  const fetcher: typeof fetch = async () => {
    calls += 1
    const response = Response.json(settings)
    response.json = () => {
      parsing.resolve()
      return body.promise
    }
    return response
  }
  const scope = createHumanGrantSessionScope(first, () => identity, fetcher)
  const work = readHumanGrantSettings(signal(), scope.fetch)

  await parsing.promise
  identity = second
  body.resolve(settings)
  await assert.rejects(work, { name: 'AbortError' })
  assert.equal(calls, 1)
})

test('session change before JSON parsing cancels the unread body', async () => {
  let identity = first
  let cancelled = 0
  const body = new ReadableStream<Uint8Array>({
    cancel() {
      cancelled += 1
    },
  })
  const fetcher: typeof fetch = async () => {
    const response = new Response(body)
    response.json = async () => settings
    return response
  }
  const scope = createHumanGrantSessionScope(first, () => identity, fetcher)
  const response = await scope.fetch('/synthetic-humangrant-settings')

  identity = second
  await assert.rejects(response.json(), { name: 'AbortError' })
  assert.equal(cancelled, 1)
})

test('observed A -> B -> A cannot revive a captured draft', async () => {
  let identity = first
  let calls = 0
  const fetcher: typeof fetch = async () => {
    calls++
    return Response.json(settings)
  }
  const scope = createHumanGrantSessionScope(first, () => identity, fetcher)
  identity = second
  assert.throws(scope.check, { name: 'AbortError' })
  identity = first
  await assert.rejects(
    writeHumanGrantSettings(settings, signal(), scope.fetch),
    { name: 'AbortError' }
  )
  assert.equal(calls, 0)
})

test('changing the captured object cannot change request ownership', () => {
  const captured = { ...first }
  const scope = createHumanGrantSessionScope(captured, () => second)
  Object.assign(captured, second)
  assert.throws(scope.check, { name: 'AbortError' })
})

test('explicit invalidation blocks the same session until a new scope is created', async () => {
  let calls = 0
  const fetcher: typeof fetch = async () => {
    calls++
    return Response.json(settings)
  }
  const scope = createHumanGrantSessionScope(first, () => first, fetcher)
  scope.invalidate()
  await assert.rejects(readHumanGrantSettings(signal(), scope.fetch), {
    name: 'AbortError',
  })
  assert.equal(calls, 0)
  const fresh = createHumanGrantSessionScope(first, () => first, fetcher)
  assert.deepEqual(
    await readHumanGrantSettings(signal(), fresh.fetch),
    settings
  )
  assert.equal(calls, 1)
})
