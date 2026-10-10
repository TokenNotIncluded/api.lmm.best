/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { spawn, execFileSync } from 'node:child_process'
import { mkdtempSync, readFileSync, writeFileSync, chmodSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createServer } from 'node:net'
import { once } from 'node:events'
import { setTimeout as delay } from 'node:timers/promises'
import { test } from 'node:test'

// Run with nginx installed. All requests and credentials are local fixtures.
test('Extore callback headers and request/referrer log exclusions work in nginx', async () => {
  const root = mkdtempSync(join(tmpdir(), 'extore-nginx-'))
  chmodSync(root, 0o755)
  const locations = readFileSync(new URL('../packaging/common/lmm-api/edge-policy/nginx/lmm-api-locations.conf', import.meta.url), 'utf8')
  const location = locations.match(/location = \/store\/manage \{[\s\S]*?\n\}/)?.[0]
  assert.ok(location)
  const maps = readFileSync(new URL('../packaging/common/lmm-api/edge-policy/nginx/http-map.conf', import.meta.url), 'utf8').split('# Use the original URI:')[1]
  assert.ok(maps)
  // Drop only the remainder of the marker's comment line, not any map directive.
  const logMaps = maps.slice(maps.indexOf('\n'))
  const portServer = createServer()
  portServer.listen(0, '127.0.0.1')
  await once(portServer, 'listening')
  const address = portServer.address()
  assert.ok(address && typeof address !== 'string')
  const port = address.port
  await new Promise((resolve) => portServer.close(resolve))
  writeFileSync(join(root, 'index.html'), '<!doctype html>Extore callback fixture')
  const log = join(root, 'access.log')
  const config = `pid ${root}/nginx.pid; error_log ${root}/error.log; events {} http { ${logMaps} server { listen 127.0.0.1:${port}; root ${root}; access_log ${log} combined if=$lmm_access_loggable; ${location} location = /asset.js { return 200 'fixture'; } } }`
  writeFileSync(join(root, 'nginx.conf'), config)
  let server
  try {
    execFileSync('nginx', ['-t', '-p', root, '-c', join(root, 'nginx.conf')], {stdio:'pipe'})
    server = spawn('nginx', ['-p', root, '-c', join(root, 'nginx.conf'), '-g', 'daemon off; master_process off;'], {stdio:'ignore'})
    const origin = `http://127.0.0.1:${port}`
    let ready = false
    for (let n = 0; n < 50; n++) {
      try { await fetch(origin + '/asset.js'); ready = true; break } catch { await delay(50) }
    }
    assert.ok(ready, 'nginx did not start')
    const callback = origin + '/store/manage?code=fixture-code&state=fixture-state&iss=fixture'
    const response = await fetch(callback)
    assert.equal(response.status, 200)
    assert.equal(response.headers.get('cache-control'), 'no-store')
    assert.equal(response.headers.get('referrer-policy'), 'no-referrer')
    assert.match(response.headers.get('content-security-policy'), /connect-src 'self'/)
    assert.match(await response.text(), /Extore callback fixture/)
    await fetch(origin + '/asset.js', {headers:{referer:callback}})
    const ordinary = await fetch(origin + '/store/manage')
    assert.equal(ordinary.headers.get('content-security-policy'), null)
    const stopped = once(server, 'exit')
    server.kill('SIGTERM')
    await stopped
    server = undefined
    const output = readFileSync(log, 'utf8')
    assert.match(output, /GET \/asset.js/)
    assert.doesNotMatch(output, /fixture-code|fixture-state|store\/manage/)
  } finally {
    if (server && server.exitCode === null) { const stopped = once(server, 'exit'); server.kill('SIGTERM'); await stopped }
    rmSync(root, {recursive:true, force:true})
  }
})
