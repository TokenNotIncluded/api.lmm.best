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
/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { afterEach, beforeEach, describe, test } from 'node:test'

import { getGravatarUrl } from './avatar'
import { getGravatarImage } from './avatar-cache'

const originalCaches = Object.getOwnPropertyDescriptor(globalThis, 'caches')
const originalFetch = globalThis.fetch
const originalNow = Date.now
const hour = 60 * 60 * 1000

class MemoryCache {
  entries = new Map<string, Response>()
  readError = false
  writeError = false

  async match(url: string) {
    if (this.readError) throw new Error('storage read blocked')
    return this.entries.get(url)?.clone()
  }

  async put(url: string, response: Response) {
    if (this.writeError) throw new Error('quota exceeded')
    this.entries.set(url, response.clone())
  }

  async keys() {
    return Array.from(this.entries.keys(), (url) => new Request(url))
  }

  async delete(request: Request) {
    return this.entries.delete(request.url)
  }
}

let cache: MemoryCache
let now: number
let requests: { url: string; options: RequestInit | undefined }[]
let respond: () => Response | Promise<Response>

function setCaches(value: unknown) {
  Object.defineProperty(globalThis, 'caches', {
    configurable: true,
    value,
  })
}

function imageResponse(body = 'avatar') {
  return new Response(body, { headers: { 'content-type': 'image/png' } })
}

async function assertImage(value: unknown, expected = 'avatar') {
  assert.ok(value instanceof Blob)
  assert.equal(value.type, 'image/png')
  assert.equal(await value.text(), expected)
}

beforeEach(() => {
  cache = new MemoryCache()
  now = 1_800_000_000_000
  requests = []
  respond = () => imageResponse()
  setCaches({ open: async () => cache })
  Date.now = () => now
  globalThis.fetch = (async (input, options) => {
    requests.push({ url: String(input), options })
    return respond()
  }) as typeof fetch
})

afterEach(() => {
  globalThis.fetch = originalFetch
  Date.now = originalNow
  if (originalCaches) {
    Object.defineProperty(globalThis, 'caches', originalCaches)
  } else {
    Reflect.deleteProperty(globalThis, 'caches')
  }
})

describe('persistent Gravatar image cache', () => {
  test('reads persisted image bytes without a network request', async () => {
    const url = await getGravatarUrl('persisted@example.com')
    assert.ok(url)
    const response = imageResponse('from a previous page load')
    response.headers.set('x-lmm-avatar-expires', String(now + hour))
    cache.entries.set(url, response)

    await assertImage(
      await getGravatarImage('persisted@example.com'),
      'from a previous page load'
    )
    assert.equal(requests.length, 0)
  })

  test('persists downloaded bytes and reuses them after the request completes', async () => {
    await assertImage(await getGravatarImage('reuse@example.com'))
    await assertImage(await getGravatarImage(' REUSE@example.com '))

    assert.equal(requests.length, 1)
    assert.equal(cache.entries.size, 1)
    assert.equal(requests[0].options?.credentials, 'omit')
    assert.equal(requests[0].options?.referrerPolicy, 'no-referrer')
  })

  test('shares one in-flight download between simultaneous consumers', async () => {
    // Hold the download until every caller has finished its asynchronous hash.
    const subtle = globalThis.crypto.subtle
    const originalDigest = subtle.digest
    const digest = await originalDigest.call(
      subtle,
      'SHA-256',
      new TextEncoder().encode('concurrent@example.com')
    )
    subtle.digest = (async () => digest) as typeof subtle.digest
    let finishDownload!: (response: Response) => void
    let requestStarted!: () => void
    const started = new Promise<void>((resolve) => {
      requestStarted = resolve
    })
    respond = () => {
      requestStarted()
      return new Promise<Response>((resolve) => {
        finishDownload = resolve
      })
    }

    try {
      const images = Promise.all([
        getGravatarImage('concurrent@example.com'),
        getGravatarImage('concurrent@example.com'),
        getGravatarImage(' CONCURRENT@example.com '),
      ])
      await started
      finishDownload(imageResponse())
      for (const value of await images) await assertImage(value)
      assert.equal(requests.length, 1)
    } finally {
      subtle.digest = originalDigest
    }
  })

  test('refreshes image bytes after 24 hours, including the expiry boundary', async () => {
    await getGravatarImage('expiry@example.com')
    now += 24 * hour - 1
    await getGravatarImage('expiry@example.com')
    assert.equal(requests.length, 1)

    now += 1
    respond = () => imageResponse('updated avatar')
    await assertImage(
      await getGravatarImage('expiry@example.com'),
      'updated avatar'
    )
    assert.equal(requests.length, 2)
  })

  test('caches a missing avatar for one hour, then discovers a newly added image', async () => {
    respond = () => new Response(null, { status: 404 })
    assert.equal(await getGravatarImage('missing@example.com'), null)
    now += hour - 1
    assert.equal(await getGravatarImage('missing@example.com'), null)
    assert.equal(requests.length, 1)

    now += 1
    respond = () => imageResponse()
    await assertImage(await getGravatarImage('missing@example.com'))
    assert.equal(requests.length, 2)
  })

  test('isolates different accounts and image sizes', async () => {
    await getGravatarImage('first@example.com', 64)
    await getGravatarImage('second@example.com', 64)
    await getGravatarImage('first@example.com', 256)
    await getGravatarImage('first@example.com', 64)

    assert.equal(requests.length, 3)
    assert.equal(new Set(requests.map(({ url }) => url)).size, 3)
    assert.equal(cache.entries.size, 3)
  })

  test('uses ordinary image loading when CacheStorage is unavailable or blocked', async () => {
    const email = 'unsupported@example.com'
    const url = await getGravatarUrl(email)
    setCaches(undefined)
    assert.equal(await getGravatarImage(email), url)
    setCaches({
      open: async () => {
        throw new Error('storage blocked')
      },
    })
    assert.equal(await getGravatarImage(email), url)
    assert.equal(requests.length, 0)
  })

  test('falls back to the remote image after a CORS or network failure and retries later', async () => {
    const email = 'cors@example.com'
    respond = () => {
      throw new TypeError('Failed to fetch')
    }
    assert.equal(await getGravatarImage(email), await getGravatarUrl(email))
    assert.equal(cache.entries.size, 0)

    respond = () => imageResponse()
    await assertImage(await getGravatarImage(email))
    assert.equal(requests.length, 2)
  })

  test('still displays downloaded images when cache reads or writes fail', async () => {
    cache.readError = true
    cache.writeError = true
    await assertImage(await getGravatarImage('quota@example.com'))
    assert.equal(requests.length, 1)
    assert.equal(cache.entries.size, 0)
  })

  test('does not persist server errors, non-image responses, or empty images', async () => {
    const responses = [
      new Response('unavailable', { status: 503 }),
      new Response('<html>error</html>', {
        headers: { 'content-type': 'text/html' },
      }),
      imageResponse(''),
    ]
    for (const response of responses) {
      respond = () => response
      assert.equal(await getGravatarImage('invalid@example.com'), null)
      assert.equal(cache.entries.size, 0)
    }

    respond = () => imageResponse()
    await assertImage(await getGravatarImage('invalid@example.com'))
    assert.equal(requests.length, 4)
  })

  test('replaces malformed persisted data with a fresh download', async () => {
    const email = 'corrupt@example.com'
    const url = await getGravatarUrl(email)
    assert.ok(url)
    cache.entries.set(
      url,
      new Response('not an image', {
        headers: {
          'content-type': 'text/plain',
          'x-lmm-avatar-expires': String(now + hour),
        },
      })
    )

    await assertImage(await getGravatarImage(email))
    assert.equal(requests.length, 1)
  })

  test('evicts the oldest entry after storing 32 avatars', async () => {
    for (let index = 0; index < 33; index += 1) {
      await getGravatarImage(`bounded-${index}@example.com`)
    }
    const oldest = await getGravatarUrl('bounded-0@example.com')
    const newest = await getGravatarUrl('bounded-32@example.com')
    assert.ok(oldest && newest)
    assert.equal(cache.entries.size, 32)
    assert.equal(cache.entries.has(oldest), false)
    assert.equal(cache.entries.has(newest), true)
  })

  test('displays oversized images without persisting them', async () => {
    const body = 'x'.repeat(512 * 1024 + 1)
    respond = () => imageResponse(body)
    const image = await getGravatarImage('oversized@example.com')
    assert.ok(image instanceof Blob)
    assert.equal(image.size, body.length)
    assert.equal(cache.entries.size, 0)
  })

  test('does not request an avatar for an empty email', async () => {
    assert.equal(await getGravatarImage('  '), null)
    assert.equal(await getGravatarImage(null), null)
    assert.equal(requests.length, 0)
    assert.equal(cache.entries.size, 0)
  })
})
