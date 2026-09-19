import assert from 'node:assert/strict'
import { execFileSync, spawn } from 'node:child_process'
import { once } from 'node:events'
import fs from 'node:fs'
import http from 'node:http'
import net from 'node:net'
import os from 'node:os'
import path from 'node:path'
import { test } from 'node:test'

const root = path.resolve(import.meta.dirname, '..')
const templates = path.join(root, 'packaging/common/lmm-api/edge-policy/nginx')

test('cluster distributes traffic, preserves streaming and never replays failed writes', async () => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'lmm-cluster-test-'))
  const attempts = []
  const servers = ['first', 'second'].map(name => {
    const server = http.createServer((request, response) => {
      if (request.url === '/internal/access-ip-policy') {
        response.writeHead(204).end()
      } else if (request.url === '/api/fail') {
        attempts.push(name)
        request.resume()
        request.on('end', () => request.socket.destroy())
      } else if (request.url === '/api/stream') {
        response.writeHead(200, { 'Content-Type': 'text/event-stream' })
        response.write('data: first\n\n')
        setTimeout(() => response.end('data: last\n\n'), 500)
      } else {
        response.writeHead(200, { 'Content-Type': 'application/json' })
        response.end(JSON.stringify({ name }))
      }
    })
    server.on('upgrade', (request, socket) => {
      socket.end('HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n')
    })
    return server
  })
  let nginx
  try {
    for (const server of servers) {
      server.listen(0, '127.0.0.1')
      await once(server, 'listening')
    }
    const reservation = net.createServer()
    reservation.listen(0, '127.0.0.1')
    await once(reservation, 'listening')
    const port = reservation.address().port
    await new Promise(resolve => reservation.close(resolve))
    const maps = fs.readFileSync(path.join(templates, 'http-map.conf'), 'utf8')
      .replace(/geoip2[^]*?\n\}/, '')
      .replaceAll('127.0.0.1:3000', `127.0.0.1:${servers[0].address().port}`)
      .replace('include /etc/lmm-api/nginx/peers/*.conf;', `server 127.0.0.1:${servers[1].address().port} max_fails=1 fail_timeout=10s;`)
    const locations = fs.readFileSync(path.join(templates, 'lmm-api-locations.conf'), 'utf8')
      .replaceAll('/var/log/nginx/access.log', `${directory}/access.log`)
      .replaceAll('/etc/nginx/lmm-api-mime.types', `${templates}/mime.types`)
    fs.writeFileSync(`${directory}/locations.conf`, locations)
    fs.writeFileSync(`${directory}/nginx.conf`, `
worker_processes 1;
pid ${directory}/nginx.pid;
error_log ${directory}/error.log;
events { worker_connections 64; }
http {
  access_log off;
  client_body_temp_path ${directory}/body;
  proxy_temp_path ${directory}/proxy;
  fastcgi_temp_path ${directory}/fastcgi;
  uwsgi_temp_path ${directory}/uwsgi;
  scgi_temp_path ${directory}/scgi;
  map $host $lmm_geoip_country_code { default US; }
  ${maps}
  server {
    listen 127.0.0.1:${port};
    include ${templates}/lmm-api-region-policy.conf;
    include ${directory}/locations.conf;
  }
}`)
    execFileSync('nginx', ['-t', '-p', directory, '-c', `${directory}/nginx.conf`], { stdio: 'pipe' })
    nginx = spawn('nginx', ['-p', directory, '-c', `${directory}/nginx.conf`, '-g', 'daemon off; master_process off;'], { stdio: 'ignore' })
    const base = `http://127.0.0.1:${port}`
    for (let i = 0; i < 50; i++) {
      try { await fetch(`${base}/api/ping`); break } catch { await new Promise(resolve => setTimeout(resolve, 50)) }
    }
    // Concurrent requests avoid correlation between the auth and main request.
    const names = await Promise.all(Array.from({ length: 20 }, async () => {
      const response = await fetch(`${base}/api/ping`)
      assert.equal(response.status, 200)
      return (await response.json()).name
    }))
    assert.deepEqual(new Set(names), new Set(['first', 'second']))

    const stream = await fetch(`${base}/api/stream`)
    const reader = stream.body.getReader()
    const first = await reader.read()
    assert.equal(new TextDecoder().decode(first.value), 'data: first\n\n')
    let rest = ''
    for (;;) {
      const chunk = await reader.read()
      if (chunk.done) break
      rest += new TextDecoder().decode(chunk.value)
    }
    assert.equal(rest, 'data: last\n\n')

    await new Promise((resolve, reject) => {
      const request = http.request(`${base}/api/ws`, { headers: { Upgrade: 'websocket', Connection: 'Upgrade' } })
      request.on('upgrade', (response, socket) => {
        assert.equal(response.statusCode, 101)
        socket.destroy()
        resolve()
      })
      request.on('response', response => reject(new Error(`Upgrade failed: ${response.statusCode}`)))
      request.on('error', reject)
      request.setTimeout(2000, () => request.destroy(new Error('Upgrade timed out')))
      request.end()
    })

    const failed = await fetch(`${base}/api/fail`, { method: 'POST', body: 'bill once' })
    assert.equal(failed.status, 503)
    await failed.text()
    assert.equal(attempts.length, 1)
    const survivor = await fetch(`${base}/api/ping`)
    assert.equal(survivor.status, 200)
    assert.notEqual((await survivor.json()).name, attempts[0])
  } finally {
    if (nginx && nginx.exitCode === null) {
      nginx.kill('SIGTERM')
      await once(nginx, 'exit')
    }
    for (const server of servers) {
      server.closeAllConnections()
      await new Promise(resolve => server.close(resolve))
    }
    fs.rmSync(directory, { recursive: true, force: true })
  }
})
