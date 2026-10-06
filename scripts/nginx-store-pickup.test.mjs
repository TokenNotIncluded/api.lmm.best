/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { spawn, execFileSync } from 'node:child_process'
import { once } from 'node:events'
import fs from 'node:fs'
import http from 'node:http'
import net from 'node:net'
import os from 'node:os'
import path from 'node:path'
import { test } from 'node:test'

const root = path.resolve(import.meta.dirname, '..')
const templates = path.join(root, 'packaging/common/lmm-api/edge-policy/nginx')

test('only pickup credentials and signed store callbacks bypass a rejecting IP policy', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'lmm-store-pickup-'))
  const frontend = path.join(dir, 'frontend')
  fs.mkdirSync(path.join(frontend, 'current'), { recursive: true })
  fs.writeFileSync(path.join(frontend, 'current/index.html'), '<!doctype html><title>Pickup fixture</title>')
  const attempts = []
  const backend = http.createServer((request, response) => {
    attempts.push(request.url)
    request.resume()
    request.on('end', () => response.writeHead(200, { 'Content-Type': 'application/json' }).end('{"success":true}'))
  })
  backend.listen(0, '127.0.0.1')
  await once(backend, 'listening')
  const reservation = net.createServer()
  reservation.listen(0, '127.0.0.1')
  await once(reservation, 'listening')
  const port = reservation.address().port
  await new Promise(resolve => reservation.close(resolve))
  const source = fs.readFileSync(path.join(templates, 'lmm-api-locations.conf'), 'utf8')
  fs.writeFileSync(path.join(dir, 'locations.conf'), source
    .replaceAll('/var/log/nginx/access.log', path.join(dir, 'access.log'))
    .replaceAll('/etc/nginx/lmm-api-mime.types', path.join(templates, 'mime.types'))
    .replaceAll('/srv/lmm-api-frontend', frontend)
    .replaceAll('http://lmm_api_backend_pool', `http://127.0.0.1:${backend.address().port}`))
  const maps = fs.readFileSync(path.join(templates, 'http-map.conf'), 'utf8')
  fs.writeFileSync(path.join(dir, 'nginx.conf'), `worker_processes 1;
pid ${dir}/nginx.pid;
error_log ${dir}/error.log;
events {worker_connections 64;}
http {
  access_log off;
  client_body_temp_path ${dir}/body;
  proxy_temp_path ${dir}/proxy;
  fastcgi_temp_path ${dir}/fastcgi;
  uwsgi_temp_path ${dir}/uwsgi;
  scgi_temp_path ${dir}/scgi;
  map $http_upgrade $websocket_upgrade {default ""; ~*^websocket$ websocket;}
  map $websocket_upgrade $connection_upgrade {default close; websocket upgrade;}
  ${maps.slice(maps.indexOf('map "$request_method:$http_accept"'))}
  server {
    listen 127.0.0.1:${port};
    auth_request /__test_auth;
    location = /__test_auth {internal; auth_request off; if ($http_x_fixture_allow = 1) {return 204;} return 403;}
    include ${dir}/locations.conf;
  }
}`)
  let nginx
  try {
    execFileSync('nginx', ['-t', '-p', dir, '-c', path.join(dir, 'nginx.conf')], { stdio: 'pipe' })
    nginx = spawn('nginx', ['-p', dir, '-c', path.join(dir, 'nginx.conf'), '-g', 'daemon off; master_process off;'], { stdio: 'ignore' })
    const base = `http://127.0.0.1:${port}`
    const token = 's'.repeat(43)
    for (let i = 0; i < 50; i++) {
      try { await fetch(`${base}/api/store/products`); break } catch { await new Promise(resolve => setTimeout(resolve, 50)) }
    }
    const page = await fetch(`${base}/store/claim/${token}`)
    assert.equal(page.status, 200)
    assert.equal(page.headers.get('cache-control'), 'no-store')
    assert.equal(page.headers.get('referrer-policy'), 'no-referrer')
    assert.match(await page.text(), /Pickup fixture/)
    for (const method of ['GET', 'POST']) {
      const response = await fetch(`${base}/api/store/claim/${token}`, { method, ...(method === 'POST' ? { body: '{"pickup_code":"secret"}' } : {}) })
      assert.equal(response.status, 200)
      assert.equal(response.headers.get('cache-control'), 'no-store')
      assert.equal(response.headers.get('referrer-policy'), 'no-referrer')
      await response.text()
    }
    for (const endpoint of [`/api/store/payments/epay/${'a'.repeat(64)}/notify`, '/api/store/payments/pancake/external/17/prod/webhook', '/api/store/payments/pancake/platform/0/test/webhook']) {
      const response = await fetch(base + endpoint, { method: 'POST', body: 'signed callback fixture' })
      assert.equal(response.status, 200, endpoint)
      await response.text()
    }
    for (const endpoint of ['/store', '/store/manage', '/api/store/products', '/api/store/config', '/api/store/payments/settings', `/store/claim/${token}x`, `/api/store/claim/${token}/settings`, '/api/store/claim/short', '/api/store/payments/pancake/external/17/live/webhook', '/api/store/payments/pancake/external/17/prod/webhook/extra']) {
      const response = await fetch(base + endpoint)
      assert.equal(response.status, 403, endpoint)
      await response.text()
      assert.ok(!attempts.includes(endpoint), endpoint)
    }
    await fetch(`${base}/api/store/products?ordinary-store-retained=1`, { headers: { 'X-Fixture-Allow': '1' } })
    await fetch(`${base}/api/store/products`, { headers: { 'X-Fixture-Allow': '1', Referer: `https://api.lmm.best/store/claim/${token}` } })
    const log = fs.readFileSync(path.join(dir, 'access.log'), 'utf8')
    assert.match(log, /ordinary-store-retained=1/)
    assert.ok(!log.includes(token), 'pickup credentials must never appear in access logs or referrers')
  } finally {
    if (nginx && nginx.exitCode === null) { nginx.kill('SIGTERM'); await once(nginx, 'exit') }
    backend.closeAllConnections()
    await new Promise(resolve => backend.close(resolve))
    fs.rmSync(dir, { recursive: true, force: true })
  }
})
