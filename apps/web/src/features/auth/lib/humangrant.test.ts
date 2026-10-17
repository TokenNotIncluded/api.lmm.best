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
import {
  HumanGrantHttpError,
  humanGrantReadErrorKey,
  isHumanGrantAvailable,
  parseHumanGrantSettings,
  readHumanGrantSettings,
  watchHumanGrantAvailability,
  writeHumanGrantSettings,
} from './humangrant'

// Main 5999dc656: adminSettings returns 200 {enabled}, not 204 or {success}.
// Transport tests only; the server still owns authorization and persistence.
const settings = { enabled: false, csrf: 'synthetic-page-proof' }
const signal = () => new AbortController().signal
const receipt = () => Response.json({ enabled: false })

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => { resolve = done })
  return { promise, resolve }
}

describe('HumanGrant main contract and recovery', () => {
  test('accepts only explicit availability, never unknown as enabled', () => {
    assert.equal(isHumanGrantAvailable({ enabled: true, ready: true }), true)
    for (const value of [null, [], {}, { enabled: true }, { enabled: 'true', ready: true },
      { enabled: true, ready: 1 }, { enabled: false, ready: true },
      { enabled: true, ready: true, success: false },
      { enabled: true, ready: true, error: 'unavailable' }]) {
      assert.equal(isHumanGrantAvailable(value), false)
    }
  })

  test('validates opaque CSRF without trimming, coercion or header control characters', () => {
    assert.deepEqual(parseHumanGrantSettings(settings), settings)
    for (const csrf of ['', ' ', 'proof\r\nheader', '\tproof', 'proof ', 'x'.repeat(513), '证明']) {
      assert.throws(() => parseHumanGrantSettings({ enabled: false, csrf }))
    }
    for (const value of [null, [], {}, { enabled: null, csrf: 'proof' },
      { enabled: 'false', csrf: 'proof' }, { enabled: false },
      { success: true, data: settings },
      { ...settings, success: false }, { ...settings, error: 'forbidden' }]) assert.throws(() => parseHumanGrantSettings(value))
  })

  test('reads human-cookie state without dashboard authorization or browser caching', async () => {
    const fetcher: typeof fetch = async (url, options) => {
      assert.equal(url, '/api/user/auth/humangrant/settings')
      assert.equal(options?.credentials, 'same-origin')
      assert.equal(options?.cache, 'no-store')
      assert.equal(options?.redirect, 'error')
      assert.equal(new Headers(options?.headers).has('authorization'), false)
      return Response.json(settings)
    }
    assert.deepEqual(await readHumanGrantSettings(signal(), fetcher), settings)
  })

  test('keeps HTTP errors distinct from empty or false settings', async () => {
    for (const status of [401, 403, 429, 500, 503]) {
      const fetcher: typeof fetch = async () => Response.json({ error: 'fixture' }, { status })
      await assert.rejects(readHumanGrantSettings(signal(), fetcher),
        (error: unknown) => error instanceof HumanGrantHttpError && error.status === status)
    }
  })

  test('renders fixed recovery messages, not response payloads', () => {
    assert.match(humanGrantReadErrorKey(new HumanGrantHttpError(401)), /root account/)
    assert.match(humanGrantReadErrorKey(new HumanGrantHttpError(403)), /root account/)
    assert.match(humanGrantReadErrorKey(new HumanGrantHttpError(429)), /Wait/)
    assert.match(humanGrantReadErrorKey(new HumanGrantHttpError(503)), /unavailable/)
    assert.equal(humanGrantReadErrorKey(new Error('private value')),
      'HumanGrant settings could not be loaded. Sign in again or retry.')
  })

  test('requires JSON content type and a non-redirected 200 read', async () => {
    const redirected = Response.json(settings)
    Object.defineProperty(redirected, 'redirected', { value: true })
    for (const response of [new Response(JSON.stringify(settings)), redirected,
      new Response('{', { headers: { 'content-type': 'application/json' } }),
      Response.json(settings, { status: 202 }), new Response(null, { status: 204 })]) {
      const fetcher: typeof fetch = async () => response
      await assert.rejects(readHumanGrantSettings(signal(), fetcher))
    }
    const fetcher: typeof fetch = async () => Response.json(settings,
      { headers: { 'content-type': 'Application/JSON; charset=utf-8' } })
    assert.deepEqual(await readHumanGrantSettings(signal(), fetcher), settings)
  })

  test('valid main receipt causes one PUT and one readback with a fresh proof', async () => {
    const methods: string[] = []
    const fresh = { ...settings, csrf: 'new-synthetic-proof' }
    const fetcher: typeof fetch = async (_url, options) => {
      methods.push(options?.method ?? 'GET')
      if (options?.method === 'PUT') {
        assert.equal(options.body, JSON.stringify({ enabled: false }))
        const headers = new Headers(options.headers)
        assert.equal(headers.get('X-HG-CSRF'), settings.csrf)
        assert.equal(headers.has('Authorization'), false)
        return receipt()
      }
      return Response.json(fresh)
    }
    assert.deepEqual(await writeHumanGrantSettings(settings, signal(), fetcher), fresh)
    assert.deepEqual(methods, ['PUT', 'GET'])
  })

  test('HTML 200 is not a receipt, even when the current state equals the draft', async () => {
    const methods: string[] = []
    const fetcher: typeof fetch = async (_url, options) => {
      methods.push(options?.method ?? 'GET')
      return options?.method === 'PUT' ? new Response('<html>Sign in</html>') : Response.json(settings)
    }
    await assert.rejects(writeHumanGrantSettings(settings, signal(), fetcher))
    assert.deepEqual(methods, ['PUT'])
  })

  test('rejects missing, contradictory and failed acknowledgements without readback', async () => {
    for (const value of [null, [], {}, { enabled: true }, { enabled: 0 },
      { success: false, enabled: false }, { error: 'failure', enabled: false }]) {
      let calls = 0
      const fetcher: typeof fetch = async () => { calls += 1; return Response.json(value) }
      await assert.rejects(writeHumanGrantSettings(settings, signal(), fetcher))
      assert.equal(calls, 1)
    }
  })

  test('pending, empty, rejected and failed PUTs are not completed saves', async () => {
    for (const status of [201, 202, 204, 401, 403, 409, 429, 500, 503]) {
      let calls = 0
      const fetcher: typeof fetch = async () => {
        calls += 1
        return status === 204 ? new Response(null, { status }) : Response.json({ enabled: false }, { status })
      }
      await assert.rejects(writeHumanGrantSettings(settings, signal(), fetcher))
      assert.equal(calls, 1)
    }
  })

  test('does not retry on a dropped PUT response', async () => {
    let calls = 0
    const fetcher: typeof fetch = async () => { calls += 1; throw new TypeError('fixture disconnect') }
    await assert.rejects(writeHumanGrantSettings(settings, signal(), fetcher))
    assert.equal(calls, 1)
  })

  test('readback failure or concurrent changes never resend the successful PUT', async () => {
    for (const readback of [() => new Response(null, { status: 503 }),
      () => Response.json({ ...settings, enabled: true })]) {
      const methods: string[] = []
      const fetcher: typeof fetch = async (_url, options) => {
        methods.push(options?.method ?? 'GET')
        return options?.method === 'PUT' ? receipt() : readback()
      }
      await assert.rejects(writeHumanGrantSettings(settings, signal(), fetcher))
      assert.deepEqual(methods, ['PUT', 'GET'])
    }
  })

  test('aborted operations and invalid drafts never call the transport', async () => {
    let calls = 0
    const fetcher: typeof fetch = async () => { calls += 1; return Response.json(settings) }
    const cancelled = new AbortController()
    cancelled.abort()
    await assert.rejects(readHumanGrantSettings(cancelled.signal, fetcher))
    await assert.rejects(writeHumanGrantSettings(settings, cancelled.signal, fetcher))
    await assert.rejects(writeHumanGrantSettings({ ...settings, csrf: '\n' }, signal(), fetcher))
    assert.equal(calls, 0)
  })

  test('abort after headers or body parsing never returns a valid setting', async () => {
    const cancelled = new AbortController()
    const response = Response.json(settings)
    response.json = async () => { cancelled.abort(); return settings }
    const fetcher: typeof fetch = async () => response
    await assert.rejects(readHumanGrantSettings(cancelled.signal, fetcher))
  })

  test('abort during receipt parsing prevents a follow-up GET', async () => {
    const cancelled = new AbortController()
    let calls = 0
    const response = receipt()
    response.json = async () => { cancelled.abort(); return { enabled: false } }
    const fetcher: typeof fetch = async () => { calls += 1; return response }
    await assert.rejects(writeHumanGrantSettings(settings, cancelled.signal, fetcher))
    assert.equal(calls, 1)
  })

  test('config is anonymous and a newer response supersedes an older one', async () => {
    const old = deferred<Response>()
    let calls = 0
    const states: boolean[] = []
    const fetcher: typeof fetch = async (url, options) => {
      assert.equal(url, '/humangrant/config')
      assert.equal(options?.credentials, 'omit')
      assert.equal(options?.cache, 'no-store')
      assert.equal(options?.redirect, 'error')
      return ++calls === 1 ? old.promise : Response.json({ enabled: false, ready: true })
    }
    const watcher = watchHumanGrantAvailability((state) => states.push(state), fetcher)
    try {
      const pending = watcher.refresh()
      await watcher.refresh()
      old.resolve(Response.json({ enabled: true, ready: true }))
      await pending
      assert.equal(states.includes(true), false)
    } finally { watcher.dispose() }
  })

  test('config stale body parsing is discarded after refresh', async () => {
    const body = deferred<unknown>(), started = deferred<void>()
    let calls = 0
    const fetcher: typeof fetch = async () => {
      if (++calls !== 1) return Response.json({ enabled: false, ready: true })
      const response = Response.json({})
      response.json = async () => { started.resolve(); return body.promise }
      return response
    }
    const states: boolean[] = []
    const watcher = watchHumanGrantAvailability((state) => states.push(state), fetcher)
    try {
      const pending = watcher.refresh()
      await started.promise
      await watcher.refresh()
      body.resolve({ enabled: true, ready: true })
      await pending
      assert.equal(states.includes(true), false)
    } finally { watcher.dispose() }
  })

  test('failed or redirected config reads hide an already visible entry', async () => {
    for (const next of [() => new Response(null, { status: 503 }),
      () => new Response(JSON.stringify({ enabled: true, ready: true }))]) {
      let calls = 0
      const states: boolean[] = []
      const fetcher: typeof fetch = async () => ++calls === 1
        ? Response.json({ enabled: true, ready: true }) : next()
      const watcher = watchHumanGrantAvailability((state) => states.push(state), fetcher)
      try {
        await watcher.refresh()
        assert.equal(states.at(-1), true)
        await watcher.refresh()
        assert.equal(states.at(-1), false)
      } finally { watcher.dispose() }
    }
  })

  test('disposed watchers cannot publish or start another request', async () => {
    const pending = deferred<Response>()
    let calls = 0
    const states: boolean[] = []
    const fetcher: typeof fetch = async () => { calls += 1; return pending.promise }
    const watcher = watchHumanGrantAvailability((state) => states.push(state), fetcher)
    const work = watcher.refresh()
    watcher.dispose()
    const count = states.length
    pending.resolve(Response.json({ enabled: true, ready: true }))
    await work
    await watcher.refresh()
    assert.equal(calls, 1)
    assert.equal(states.length, count)
  })
})
