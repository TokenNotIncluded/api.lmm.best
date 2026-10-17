// SPDX-License-Identifier: AGPL-3.0-only
// Real Chromium + HTTP tests of the production client module. The HTTP provider
// is synthetic: this is NOT React-page, lmm-core, PostgreSQL or grant acceptance.
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import http from 'node:http'
import { createRequire } from 'node:module'
import path from 'node:path'
import { after, before, beforeEach, describe, test } from 'node:test'
import { pathToFileURL } from 'node:url'

const modulePath = process.env.LMM_A10_CLIENT_MODULE
assert.ok(modulePath && path.isAbsolute(modulePath), 'Set the absolute compiled client module path')
const clientSource = await readFile(modulePath)
const require = createRequire(import.meta.url)
const entry = process.env.PLAYWRIGHT_MODULE ?? require.resolve('playwright')
assert.ok(path.isAbsolute(entry), 'Use an absolute installed Playwright entry')
const { chromium } = await import(pathToFileURL(entry).href)
const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE
if (executablePath) assert.ok(path.isAbsolute(executablePath))

const delay = (ms) => new Promise((done) => setTimeout(done, ms))
function gate() {
  let release
  const promise = new Promise((done) => { release = done })
  return { promise, release }
}
async function until(check) {
  const deadline = Date.now() + 2500
  while (!check()) {
    assert.ok(Date.now() < deadline, 'Fixture request did not arrive')
    await delay(10)
  }
}
function json(res, value, status = 200) {
  res.writeHead(status, { 'content-type': 'application/json', 'cache-control': 'no-store' })
  res.end(JSON.stringify(value))
}
async function listen(server) {
  server.requestTimeout = 3000
  server.headersTimeout = 3000
  server.keepAliveTimeout = 100
  await new Promise((done, fail) => server.once('error', fail).listen(0, '127.0.0.1', done))
  const port = server.address().port
  assert.ok(![18080, 18081, 50837, 47399].includes(port), 'Protected port selected')
  return `http://127.0.0.1:${port}`
}
async function close(server) {
  if (!server) return
  server.closeAllConnections()
  await new Promise((done) => server.close(done))
}
let browser, server, sentinel, origin, otherOrigin, fixture, hits = 0
let context, page

before(async () => {
  sentinel = http.createServer((_req, res) => { hits += 1; res.end('unexpected') })
  otherOrigin = await listen(sentinel)
  server = http.createServer((req, res) => {
    void (async () => {
      const url = new URL(req.url, origin)
      if (url.pathname === '/client.mjs') {
        res.writeHead(200, { 'content-type': 'text/javascript', 'cache-control': 'no-store' })
        return res.end(clientSource)
      }
      if (url.pathname === '/' || url.pathname === '/fixture-login') {
        res.writeHead(200, {
          'content-type': 'text/html', 'cache-control': 'no-store',
          ...(url.pathname === '/fixture-login'
            ? { 'set-cookie': 'a10_human=synthetic; HttpOnly; SameSite=Strict; Path=/' } : {}),
        })
        return res.end('<!doctype html><title>A10 synthetic transport fixture</title>')
      }
      if (url.pathname === '/favicon.ico') { res.writeHead(204); return res.end() }
      // No request payloads, cookies, CSRF values or arbitrary URLs enter logs.
      const record = { method: req.method, path: url.pathname,
        cookie: Boolean(req.headers.cookie), bearer: Boolean(req.headers.authorization) }
      fixture.requests.push(record)
      assert.ok(fixture.requests.length <= 100, 'Fixture request budget exceeded')
      if (url.pathname === '/humangrant/config') {
        fixture.configReads += 1
        if (fixture.oldConfig && fixture.configReads === 1) {
          await fixture.oldConfig.promise
          return json(res, { enabled: true, ready: true })
        }
        return json(res, { enabled: fixture.enabled, ready: true })
      }
      if (url.pathname !== '/api/user/auth/humangrant/settings') return json(res, {}, 404)
      if (!req.headers.cookie?.includes('a10_human=synthetic')) return json(res, {}, 403)
      if (req.method === 'GET') {
        fixture.reads += 1
        if (fixture.readStatus !== 200) return json(res, {}, fixture.readStatus)
        fixture.csrf = `synthetic-proof-${fixture.reads}`
        return json(res, { enabled: fixture.enabled, csrf: fixture.csrf })
      }
      if (req.method !== 'PUT') return json(res, {}, 405)
      fixture.puts += 1
      if (req.headers['x-hg-csrf'] !== fixture.csrf || req.headers.origin !== origin) return json(res, {}, 403)
      let raw = ''
      for await (const chunk of req) {
        raw += chunk
        if (Buffer.byteLength(raw) > 16384) return json(res, {}, 413)
      }
      const body = JSON.parse(raw)
      if (typeof body.enabled !== 'boolean') return json(res, {}, 400)
      if (fixture.mode === 'html') {
        res.writeHead(200, { 'content-type': 'text/html' })
        return res.end('<html>Not a save receipt</html>')
      }
      if (fixture.mode === 'failed-json') return json(res, { success: false, enabled: body.enabled })
      if (fixture.mode === 'redirect') {
        res.writeHead(302, { location: `${otherOrigin}/sentinel` }); return res.end()
      }
      fixture.enabled = body.enabled
      fixture.writes += 1
      if (fixture.mode === 'drop') return res.destroy()
      if (fixture.ack) await fixture.ack.promise
      if (fixture.mode === 'readback-unavailable') fixture.readStatus = 503
      return json(res, { enabled: fixture.enabled })
    })().catch(() => { if (!res.writableEnded) res.destroy() })
  })
  origin = await listen(server)
  browser = await chromium.launch({ headless: true, ...(executablePath ? { executablePath } : {}) })
  console.log(JSON.stringify({ scope: 'synthetic HTTP provider; real production browser client',
    node: process.version, chromium: browser.version(), client_sha256:
      createHash('sha256').update(clientSource).digest('hex') }))
})

beforeEach(async () => {
  if (context) await context.close()
  fixture?.oldConfig?.release()
  fixture?.ack?.release()
  fixture = { enabled: true, csrf: '', requests: [], configReads: 0, reads: 0, puts: 0,
    writes: 0, readStatus: 200, mode: 'normal' }
  context = await browser.newContext({ viewport: { width: 320, height: 740 }, serviceWorkers: 'block' })
  page = await context.newPage()
  await page.goto(`${origin}/fixture-login`)
  await page.evaluate(async () => { window.client = await import('/client.mjs') })
})

after(async () => {
  fixture?.oldConfig?.release()
  fixture?.ack?.release()
  await context?.close()
  await browser?.close()
  await close(server)
  await close(sentinel)
})

async function load() {
  await page.evaluate(async () => {
    window.settings = await window.client.readHumanGrantSettings(new AbortController().signal)
  })
}
async function save() {
  return page.evaluate(async () => {
    try {
      await window.client.writeHumanGrantSettings({ ...window.settings, enabled: false }, new AbortController().signal)
      return 'confirmed'
    } catch { return 'not-confirmed' }
  })
}

describe('production HumanGrant client over real browser HTTP', { timeout: 30000 }, () => {
  test('public config omits a real browser cookie and never attaches Bearer', async () => {
    const available = await page.evaluate(async () => {
      let current
      const watcher = window.client.watchHumanGrantAvailability((v) => { current = v })
      try { await watcher.refresh(); return current } finally { watcher.dispose() }
    })
    assert.equal(available, true)
    assert.deepEqual(fixture.requests, [{ method: 'GET', path: '/humangrant/config', cookie: false, bearer: false }])
  })

  test('human-cookie GET and CSRF PUT run once, then read back; HttpOnly stays hidden', async () => {
    await load()
    assert.equal(await page.evaluate(() => document.cookie.includes('a10_human')), false)
    assert.equal(await save(), 'confirmed')
    assert.equal(fixture.writes, 1)
    assert.deepEqual(fixture.requests.map((r) => r.method), ['GET', 'PUT', 'GET'])
    assert.ok(fixture.requests.every((r) => r.cookie && !r.bearer))
    assert.equal(fixture.enabled, false)
  })

  test('anonymous settings refusal is an error, never a false setting', async () => {
    await context.clearCookies()
    const result = await page.evaluate(async () => {
      try { await window.client.readHumanGrantSettings(new AbortController().signal); return 'unexpected' }
      catch (error) { return { status: error.status, message: window.client.humanGrantReadErrorKey(error) } }
    })
    assert.equal(result.status, 403)
    assert.match(result.message, /root account/)
  })

  test('503 is unavailable, not a zero/default/empty setting', async () => {
    fixture.readStatus = 503
    const result = await page.evaluate(async () => {
      try { await window.client.readHumanGrantSettings(new AbortController().signal); return 'unexpected' }
      catch (error) { return { status: error.status, message: window.client.humanGrantReadErrorKey(error) } }
    })
    assert.equal(result.status, 503)
    assert.match(result.message, /unavailable/)
  })

  test('stale CSRF fails without automatic refresh or retry', async () => {
    await load()
    fixture.csrf = 'synthetic-replaced-proof'
    assert.equal(await save(), 'not-confirmed')
    assert.deepEqual(fixture.requests.map((r) => r.method), ['GET', 'PUT'])
    assert.equal(fixture.writes, 0)
  })

  test('HTML and failure JSON acknowledgements cannot claim save success', async () => {
    for (const mode of ['html', 'failed-json']) {
      fixture.mode = mode
      await load()
      const start = fixture.requests.length
      assert.equal(await save(), 'not-confirmed')
      assert.deepEqual(fixture.requests.slice(start).map((r) => r.method), ['PUT'])
    }
    assert.equal(fixture.writes, 0)
  })

  test('connection loss after a write never replays the PUT', async () => {
    await load()
    fixture.mode = 'drop'
    assert.equal(await save(), 'not-confirmed')
    assert.equal(fixture.writes, 1)
    assert.equal(fixture.enabled, false)
    assert.deepEqual(fixture.requests.map((r) => r.method), ['GET', 'PUT'])
  })

  test('failed readback cannot turn one write into two', async () => {
    await load()
    fixture.mode = 'readback-unavailable'
    assert.equal(await save(), 'not-confirmed')
    assert.equal(fixture.writes, 1)
    assert.deepEqual(fixture.requests.map((r) => r.method), ['GET', 'PUT', 'GET'])
  })

  test('redirect is refused before contacting a second origin', async () => {
    await load()
    fixture.mode = 'redirect'
    const before = hits
    assert.equal(await save(), 'not-confirmed')
    assert.equal(hits, before)
    assert.equal(fixture.writes, 0)
    assert.deepEqual(fixture.requests.map((r) => r.method), ['GET', 'PUT'])
  })

  test('cancel after server accepts a write does not report rollback or retry', async () => {
    await load()
    fixture.ack = gate()
    await page.evaluate(() => {
      window.cancelWrite = new AbortController()
      window.pendingWrite = window.client.writeHumanGrantSettings(
        { ...window.settings, enabled: false }, window.cancelWrite.signal
      ).then(() => 'confirmed', () => 'not-confirmed')
    })
    await until(() => fixture.writes === 1)
    await page.evaluate(() => window.cancelWrite.abort())
    assert.equal(await page.evaluate(() => window.pendingWrite), 'not-confirmed')
    fixture.ack.release()
    assert.equal(fixture.enabled, false)
    assert.equal(fixture.writes, 1)
    assert.deepEqual(fixture.requests.map((r) => r.method), ['GET', 'PUT'])
  })

  test('late config cannot restore an entry after a newer disabled state', async () => {
    fixture.oldConfig = gate()
    fixture.enabled = false
    await page.evaluate(() => {
      window.states = []
      window.watcher = window.client.watchHumanGrantAvailability((v) => window.states.push(v))
      window.oldRefresh = window.watcher.refresh()
    })
    await until(() => fixture.configReads === 1)
    await page.evaluate(() => window.watcher.refresh())
    fixture.oldConfig.release()
    await page.evaluate(() => window.oldRefresh)
    assert.equal(await page.evaluate(() => window.states.includes(true)), false)
    await page.evaluate(() => window.watcher.dispose())
  })
})
