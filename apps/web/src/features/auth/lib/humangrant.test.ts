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
  HUMAN_GRANT_CONFIG,
  HUMAN_GRANT_SETTINGS,
  isHumanGrantAvailable,
  parseHumanGrantSettings,
  readHumanGrantSettings,
  watchHumanGrantAvailability,
  writeHumanGrantSettings,
} from './humangrant'

// These are synthetic transport tests, not browser, authorization, or database tests.
const signal = () => new AbortController().signal
const settings = { enabled: false, csrf: 'synthetic-page-proof' }

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

describe('HumanGrant compatibility client', () => {
  test('requires explicit enabled and ready booleans', () => {
    assert.equal(isHumanGrantAvailable({ enabled: true, ready: true }), true)
    for (const value of [
      null, undefined, [], {}, { enabled: true },
      { enabled: false, ready: true }, { enabled: true, ready: false },
      { enabled: 'true', ready: true }, { enabled: true, ready: 1 },
      { success: true, data: { enabled: true, ready: true } },
    ]) {
      assert.equal(isHumanGrantAvailable(value), false)
    }
  })

  test('does not turn missing settings or an empty CSRF proof into defaults', () => {
    assert.deepEqual(parseHumanGrantSettings(settings), settings)
    for (const value of [
      null, [], {}, { enabled: null, csrf: 'proof' },
      { enabled: 'false', csrf: 'proof' }, { enabled: false },
      { enabled: false, csrf: '' }, { enabled: false, csrf: '  ' },
      { success: true, data: settings },
    ]) {
      assert.throws(() => parseHumanGrantSettings(value))
    }
  })

  test('reads via the human cookie route, not a dashboard Bearer token', async () => {
    const fetcher: typeof fetch = async (url, options) => {
      assert.equal(url, HUMAN_GRANT_SETTINGS)
      assert.equal(options?.credentials, 'same-origin')
      assert.equal(options?.cache, 'no-store')
      assert.equal(options?.redirect, 'error')
      assert.equal(new Headers(options?.headers).has('Authorization'), false)
      return Response.json(settings)
    }
    assert.deepEqual(await readHumanGrantSettings(signal(), fetcher), settings)
  })

  test('rejects 401, 403 and 503 rather than returning empty settings', async () => {
    for (const status of [401, 403, 503]) {
      const fetcher: typeof fetch = async () => Response.json(settings, { status })
      await assert.rejects(readHumanGrantSettings(signal(), fetcher))
    }
  })

  test('rejects HTML and invalid JSON on an apparently successful GET', async () => {
    for (const body of ['<html>sign in</html>', '{']) {
      const fetcher: typeof fetch = async () => new Response(body)
      await assert.rejects(readHumanGrantSettings(signal(), fetcher))
    }
  })

  test('performs one PUT with CSRF, then reads authoritative settings', async () => {
    const methods: string[] = []
    const fresh = { enabled: false, csrf: 'synthetic-fresh-proof' }
    const fetcher: typeof fetch = async (url, options) => {
      assert.equal(url, HUMAN_GRANT_SETTINGS)
      assert.equal(options?.credentials, 'same-origin')
      assert.equal(options?.redirect, 'error')
      assert.equal(options?.cache, 'no-store')
      methods.push(options?.method ?? 'GET')
      if (options?.method === 'PUT') {
        const headers = new Headers(options.headers)
        assert.equal(headers.get('X-HG-CSRF'), settings.csrf)
        assert.equal(headers.has('Authorization'), false)
        assert.equal(options.body, JSON.stringify({ enabled: false }))
        return new Response(null, { status: 204 })
      }
      return Response.json(fresh)
    }
    assert.deepEqual(await writeHumanGrantSettings(settings, signal(), fetcher), fresh)
    assert.deepEqual(methods, ['PUT', 'GET'])
  })

  test('never sends malformed state or an empty CSRF proof', async () => {
    let calls = 0
    const fetcher: typeof fetch = async () => {
      calls += 1
      return Response.json(settings)
    }
    await assert.rejects(writeHumanGrantSettings({ enabled: true, csrf: '' }, signal(), fetcher))
    assert.equal(calls, 0)
  })

  test('does not retry or GET after a lost PUT response', async () => {
    let calls = 0
    const fetcher: typeof fetch = async () => {
      calls += 1
      throw new TypeError('synthetic transport failure')
    }
    await assert.rejects(writeHumanGrantSettings(settings, signal(), fetcher))
    assert.equal(calls, 1)
  })

  test('does not retry a rejected PUT, including auth failures', async () => {
    for (const status of [401, 403, 409, 503]) {
      let calls = 0
      const fetcher: typeof fetch = async () => {
        calls += 1
        return new Response(null, { status })
      }
      await assert.rejects(writeHumanGrantSettings(settings, signal(), fetcher))
      assert.equal(calls, 1)
    }
  })

  test('does not replay successful PUT after failed GET readback', async () => {
    const methods: string[] = []
    const fetcher: typeof fetch = async (_url, options) => {
      methods.push(options?.method ?? 'GET')
      return new Response(null, { status: methods.length === 1 ? 204 : 503 })
    }
    await assert.rejects(writeHumanGrantSettings(settings, signal(), fetcher))
    assert.deepEqual(methods, ['PUT', 'GET'])
  })

  test('does not declare success if readback contradicts requested state', async () => {
    const fetcher: typeof fetch = async (_url, options) => options?.method === 'PUT'
      ? new Response(null, { status: 204 })
      : Response.json({ ...settings, enabled: true })
    await assert.rejects(writeHumanGrantSettings(settings, signal(), fetcher))
  })

  test('never restores button from older focus response', async () => {
    const first = deferred<Response>()
    const second = deferred<Response>()
    let calls = 0
    const states: boolean[] = []
    const signals: AbortSignal[] = []
    const fetcher: typeof fetch = async (url, options) => {
      assert.equal(url, HUMAN_GRANT_CONFIG)
      assert.equal(options?.credentials, 'omit')
      assert.equal(options?.cache, 'no-store')
      assert.equal(options?.redirect, 'error')
      assert.ok(options?.signal)
      signals.push(options.signal)
      return ++calls === 1 ? first.promise : second.promise
    }
    const watcher = watchHumanGrantAvailability((value) => states.push(value), fetcher)
    try {
      const old = watcher.refresh()
      const latest = watcher.refresh()
      assert.equal(signals[0].aborted, true)
      second.resolve(Response.json({ enabled: false, ready: true }))
      await latest
      first.resolve(Response.json({ enabled: true, ready: true }))
      await old
      assert.equal(states.includes(true), false)
    } finally {
      watcher.dispose()
    }
  })

  test('ignores stale response body parsing after a newer request', async () => {
    const body = deferred<unknown>()
    const parsing = deferred<void>()
    let calls = 0
    const fetcher: typeof fetch = async () => {
      if (++calls !== 1) return Response.json({ enabled: false, ready: true })
      const response = new Response('{}')
      response.json = () => {
        parsing.resolve()
        return body.promise
      }
      return response
    }
    const states: boolean[] = []
    const watcher = watchHumanGrantAvailability((value) => states.push(value), fetcher)
    try {
      const old = watcher.refresh()
      await parsing.promise
      await watcher.refresh()
      body.resolve({ enabled: true, ready: true })
      await old
      assert.equal(states.includes(true), false)
    } finally {
      watcher.dispose()
    }
  })

  test('clears previously visible button after latest failed check', async () => {
    let calls = 0
    const states: boolean[] = []
    const fetcher: typeof fetch = async () => ++calls === 1
      ? Response.json({ enabled: true, ready: true })
      : new Response(null, { status: 503 })
    const watcher = watchHumanGrantAvailability((value) => states.push(value), fetcher)
    try {
      await watcher.refresh()
      assert.equal(states.at(-1), true)
      await watcher.refresh()
      assert.equal(states.at(-1), false)
    } finally {
      watcher.dispose()
    }
  })

  test('dispose stops later updates and requests', async () => {
    let calls = 0
    const pending = deferred<Response>()
    const fetcher: typeof fetch = async () => {
      calls += 1
      return pending.promise
    }
    const states: boolean[] = []
    const watcher = watchHumanGrantAvailability((value) => states.push(value), fetcher)
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
