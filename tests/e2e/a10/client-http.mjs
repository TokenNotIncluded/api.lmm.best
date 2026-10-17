/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
// Real loopback TCP with a synthetic HTTP provider, not the LMM backend.
// The adapter below supplies synthetic Cookie/Origin headers. It is not a
// browser cookie jar and does not prove HttpOnly, SameSite or server authority.
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import { createServer } from 'node:http'
import path from 'node:path'
import { test } from 'node:test'
import { pathToFileURL } from 'node:url'

const modulePath = process.env.LMM_A10_CLIENT_MODULE
if (!modulePath || !path.isAbsolute(modulePath)) {
  throw new Error('LMM_A10_CLIENT_MODULE must name an absolute local HumanGrant client module')
}
const { readHumanGrantSettings, writeHumanGrantSettings, watchHumanGrantAvailability } =
  await import(pathToFileURL(modulePath).href)
console.log(JSON.stringify({
  scope: 'production client, real TCP, synthetic provider and Cookie/Origin adapter',
  node: process.version,
  client_sha256: createHash('sha256').update(await readFile(modulePath)).digest('hex'),
}))

function deferred() {
  let resolve
  const promise = new Promise((done) => { resolve = done })
  return { promise, resolve }
}

async function fixture(t) {
  const state = {
    enabled: true, reads: 0, writes: 0, requests: [], mode: 'normal',
    proof: 'synthetic-initial-proof', accepted: deferred(), release: deferred(),
    configAccepted: deferred(), releaseConfig: deferred(), configReads: 0,
  }
  let origin
  const json = (res, value, status = 200) => {
    res.writeHead(status, { 'content-type': 'application/json', 'cache-control': 'no-store' })
    res.end(JSON.stringify(value))
  }
  const server = createServer((req, res) => {
    void (async () => {
      const route = new URL(req.url, origin).pathname
      state.requests.push({
        method: req.method, path: route,
        cookie: Boolean(req.headers.cookie), bearer: Boolean(req.headers.authorization),
      })
      if (state.requests.length > 64) return json(res, {}, 429)
      if (route === '/humangrant/config') {
        state.configReads += 1
        if (state.mode === 'delayed-config' && state.configReads === 1) {
          state.configAccepted.resolve()
          await state.releaseConfig.promise
          return json(res, { enabled: true, ready: true })
        }
        return json(res, { enabled: state.enabled, ready: true })
      }
      if (route !== '/api/user/auth/humangrant/settings') return json(res, {}, 404)
      if (req.headers.cookie !== 'a10_human=synthetic') return json(res, {}, 403)
      if (req.method === 'GET') {
        state.reads += 1
        if (state.mode === 'readback-failure' && state.writes) return json(res, {}, 503)
        state.proof = `synthetic-read-proof-${state.reads}`
        return json(res, { enabled: state.enabled, csrf: state.proof })
      }
      if (req.method !== 'PUT') return json(res, {}, 405)
      if (req.headers.origin !== origin || req.headers['x-hg-csrf'] !== state.proof) {
        return json(res, {}, 403)
      }
      let raw = ''
      for await (const chunk of req) {
        raw += chunk
        if (Buffer.byteLength(raw) > 16_384) return json(res, {}, 413)
      }
      const body = JSON.parse(raw)
      if (typeof body.enabled !== 'boolean') return json(res, {}, 400)
      if (state.mode === 'html') {
        res.writeHead(200, { 'content-type': 'text/html' })
        return res.end('<html>Sign in</html>')
      }
      if (state.mode === 'failed-json') {
        return json(res, { enabled: body.enabled, success: false })
      }
      if (state.mode === 'redirect') {
        res.writeHead(307, { location: `${origin}/forbidden-redirect` })
        return res.end()
      }
      state.enabled = body.enabled
      state.writes += 1
      state.accepted.resolve()
      if (state.mode === 'drop') return res.destroy()
      if (state.mode === 'delay') await state.release.promise
      return json(res, { enabled: state.enabled })
    })().catch(() => res.destroy())
  })
  t.after(async () => {
    state.release.resolve()
    state.releaseConfig.resolve()
    if (server.listening) {
      await new Promise((resolve) => {
        server.close(resolve)
        server.closeAllConnections()
      })
    }
  })
  await new Promise((resolve, reject) => {
    server.once('error', reject)
    server.listen(0, '127.0.0.1', resolve)
  })
  const address = server.address()
  assert.ok(address && typeof address !== 'string')
  assert.ok(![18080, 18081, 50837, 47399].includes(address.port))
  origin = `http://127.0.0.1:${address.port}`
  // Only relative, fixed client paths can reach this synthetic origin.
  const transport = (authenticated = true) => (input, init) => {
    assert.equal(typeof input, 'string')
    assert.ok(['/humangrant/config', '/api/user/auth/humangrant/settings'].includes(input))
    const headers = new Headers(init?.headers)
    if (authenticated && init?.credentials === 'same-origin') {
      headers.set('Cookie', 'a10_human=synthetic')
      if (init.method === 'PUT') headers.set('Origin', origin)
    }
    return fetch(new URL(input, origin), { ...init, headers })
  }
  return { state, fetcher: transport(), anonymous: transport(false) }
}
const signal = () => AbortSignal.timeout(3_000)
const methods = (state) => state.requests.map((r) => r.method)

// No tests run against production. Timeouts and request budgets bound all work.
test('TCP config is anonymous; settings use one Cookie/CSRF PUT and a fresh read', { timeout: 6000 }, async (t) => {
  const { state, fetcher } = await fixture(t)
  let available
  const watcher = watchHumanGrantAvailability((value) => { available = value }, fetcher)
  try { await watcher.refresh() } finally { watcher.dispose() }
  assert.equal(available, true)
  assert.equal(state.requests[0].cookie, false)
  const before = await readHumanGrantSettings(signal(), fetcher)
  const after = await writeHumanGrantSettings({ ...before, enabled: false }, signal(), fetcher)
  assert.equal(after.enabled, false)
  assert.notEqual(after.csrf, before.csrf)
  assert.equal(state.writes, 1)
  assert.deepEqual(methods(state), ['GET', 'GET', 'PUT', 'GET'])
  assert.ok(state.requests.every((r) => !r.bearer))
})

test('TCP 403 and stale CSRF are failures, not empty settings or write retries', { timeout: 6000 }, async (t) => {
  const { state, fetcher, anonymous } = await fixture(t)
  await assert.rejects(readHumanGrantSettings(signal(), anonymous), { status: 403 })
  const settings = await readHumanGrantSettings(signal(), fetcher)
  state.proof = 'synthetic-rotated-proof'
  await assert.rejects(writeHumanGrantSettings({ ...settings, enabled: false }, signal(), fetcher), { status: 403 })
  assert.equal(state.writes, 0)
  assert.deepEqual(methods(state), ['GET', 'GET', 'PUT'])
})

test('TCP HTML and failed JSON acknowledgements never confirm or cause readback', { timeout: 6000 }, async (t) => {
  const { state, fetcher } = await fixture(t)
  for (const mode of ['html', 'failed-json']) {
    const settings = await readHumanGrantSettings(signal(), fetcher)
    state.mode = mode
    const start = state.requests.length
    await assert.rejects(writeHumanGrantSettings({ ...settings, enabled: false }, signal(), fetcher))
    assert.deepEqual(methods(state).slice(start), ['PUT'])
  }
  assert.equal(state.writes, 0)
})

test('TCP lost acknowledgement keeps the write unknown and never repeats it', { timeout: 6000 }, async (t) => {
  const { state, fetcher } = await fixture(t)
  const settings = await readHumanGrantSettings(signal(), fetcher)
  state.mode = 'drop'
  await assert.rejects(writeHumanGrantSettings({ ...settings, enabled: false }, signal(), fetcher))
  assert.equal(state.enabled, false)
  assert.equal(state.writes, 1)
  assert.deepEqual(methods(state), ['GET', 'PUT'])
  state.mode = 'normal'
  assert.equal((await readHumanGrantSettings(signal(), fetcher)).enabled, false)
  assert.equal(state.writes, 1)
})

test('TCP failed readback does not replay an acknowledged PUT', { timeout: 6000 }, async (t) => {
  const { state, fetcher } = await fixture(t)
  const settings = await readHumanGrantSettings(signal(), fetcher)
  state.mode = 'readback-failure'
  await assert.rejects(writeHumanGrantSettings({ ...settings, enabled: false }, signal(), fetcher), { status: 503 })
  assert.equal(state.writes, 1)
  assert.deepEqual(methods(state), ['GET', 'PUT', 'GET'])
})

test('TCP redirect never reaches the next path or forwards proof', { timeout: 6000 }, async (t) => {
  const { state, fetcher } = await fixture(t)
  const settings = await readHumanGrantSettings(signal(), fetcher)
  state.mode = 'redirect'
  await assert.rejects(writeHumanGrantSettings({ ...settings, enabled: false }, signal(), fetcher))
  assert.equal(state.writes, 0)
  assert.equal(state.requests.some((r) => r.path === '/forbidden-redirect'), false)
  assert.deepEqual(methods(state), ['GET', 'PUT'])
})

test('TCP cancellation after acceptance is not a rollback or permission to retry', { timeout: 6000 }, async (t) => {
  const { state, fetcher } = await fixture(t)
  const settings = await readHumanGrantSettings(signal(), fetcher)
  state.mode = 'delay'
  const controller = new AbortController()
  const result = writeHumanGrantSettings({ ...settings, enabled: false }, controller.signal, fetcher)
    .then(() => 'confirmed', () => 'unknown')
  await state.accepted.promise
  controller.abort()
  assert.equal(await result, 'unknown')
  state.release.resolve()
  assert.equal(state.enabled, false)
  assert.equal(state.writes, 1)
  assert.deepEqual(methods(state), ['GET', 'PUT'])
})

test('TCP old delayed config cannot restore a newer disabled entry', { timeout: 6000 }, async (t) => {
  const { state, fetcher } = await fixture(t)
  state.mode = 'delayed-config'
  state.enabled = false
  const states = []
  const watcher = watchHumanGrantAvailability((value) => states.push(value), fetcher)
  try {
    const old = watcher.refresh()
    await state.configAccepted.promise
    await watcher.refresh()
    state.releaseConfig.resolve()
    await old
    assert.equal(states.includes(true), false)
    assert.equal(state.configReads, 2)
  } finally { watcher.dispose() }
})
