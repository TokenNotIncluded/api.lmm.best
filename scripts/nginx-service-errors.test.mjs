/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import http from 'node:http'
import net from 'node:net'
import { once } from 'node:events'
import { spawn, execFileSync } from 'node:child_process'
import { test } from 'node:test'

const root = path.resolve(import.meta.dirname, '..')
test('nginx preserves access boundaries and API bodies while explaining edge outages', async () => {
 const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'lmm-nginx-outage-'))
 const frontend = path.join(dir, 'frontend');fs.mkdirSync(frontend)
 fs.mkdirSync(path.join(frontend,'current'));fs.writeFileSync(path.join(frontend,'current','index.html'), '<!doctype html><title>Fixture</title>')
 fs.writeFileSync(path.join(frontend,'service-status.json'), JSON.stringify({state:'maintenance'}))
 const backend = http.createServer((req,res) => {if(req.url==='/api/scripts'||req.url.startsWith('/scripts/')||/^\/api\/scripts\/[^/]+\/raw(?:\?|$)/.test(req.url)){res.writeHead(200,{'Content-Type':'text/plain'});res.end('public script fixture');return;}if(req.url.startsWith('/.well-known/oauth-')){res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({issuer:'https://api.lmm.best'}));return;}res.writeHead(500,{'Content-Type':'application/json'});res.end('{"error":{"message":"upstream test failure"}}')})
 backend.listen(0,'127.0.0.1');await once(backend,'listening')
 const reservation=net.createServer();reservation.listen(0,'127.0.0.1');await once(reservation,'listening');const port=reservation.address().port;await new Promise(resolve=>reservation.close(resolve))
 const source=fs.readFileSync(path.join(root,'packaging/common/lmm-api/edge-policy/nginx/lmm-api-locations.conf'),'utf8')
 fs.writeFileSync(path.join(dir,'locations.conf'),source.replaceAll('/var/log/nginx/access.log',path.join(dir,'access.log')).replaceAll('/etc/nginx/lmm-api-mime.types',path.join(root,'packaging/common/lmm-api/edge-policy/nginx/mime.types')).replaceAll('/srv/lmm-api-frontend',frontend).replaceAll('http://lmm_api_backend_pool',`http://127.0.0.1:${backend.address().port}`))
 const maps=fs.readFileSync(path.join(root,'packaging/common/lmm-api/edge-policy/nginx/http-map.conf'),'utf8')
 const map=maps.slice(maps.indexOf('map "$request_method:$http_accept"'))
 fs.writeFileSync(path.join(dir,'nginx.conf'),`worker_processes 1;
pid ${dir}/nginx.pid;
error_log ${dir}/error.log;
events {worker_connections 64;}
http {
 access_log off;
 proxy_intercept_errors on;
 client_body_temp_path ${dir}/body;
 proxy_temp_path ${dir}/proxy;
 fastcgi_temp_path ${dir}/fastcgi;
 uwsgi_temp_path ${dir}/uwsgi;
 scgi_temp_path ${dir}/scgi;
 map $http_upgrade $websocket_upgrade {default ""; ~*^websocket$ websocket;}
 map $websocket_upgrade $connection_upgrade {default close; websocket upgrade;}
 ${map}
 server {
  listen 127.0.0.1:${port};
  auth_request /__test_auth;
  location = /__test_auth {internal;auth_request off;if ($http_x_fixture_reject = 1) {return 403;}if ($http_x_fixture_allow = 1) {return 204;}return 500;}
  include ${dir}/locations.conf;
 }
}`)
 let process
 try {
  execFileSync('nginx',['-t','-p',dir,'-c',path.join(dir,'nginx.conf')],{stdio:'pipe'})
  process=spawn('nginx',['-p',dir,'-c',path.join(dir,'nginx.conf'),'-g','daemon off; master_process off;'],{stdio:'ignore'})
  const url=`http://127.0.0.1:${port}`
  let response
  for(let i=0;i<50;i++){
   try{response=await fetch(url,{headers:{Accept:'text/html'},signal:AbortSignal.timeout(1000)});break}catch{await new Promise(resolve=>setTimeout(resolve,50))}
  }
  assert.equal(response?.status,503);assert.match(response.headers.get('content-type'),/text\/html/)
  const html=await response.text();assert.ok(html.includes('LMM Best'));assert.ok(html.includes('恢复时间尚未确定'));assert.ok(!html.includes('$lmm_error_html_'));const requestID=html.match(/<code>([a-f0-9]{32})<\/code>/)[1];assert.equal(html,fs.readFileSync(path.join(root,'packaging/common/lmm-api/edge-policy/service-unavailable.html'),'utf8').trimEnd().replace('$request_id',requestID))
  // The public paths must survive named-location redirects while the
  // inherited auth_request is failing. Administrative APIs stay gated.
  for(const endpoint of ['/scripts','/scripts/','/scripts/install.sh','/api/scripts','/api/scripts/install.sh/raw','/api/scripts/install.sh/raw?download=1']) {
   response=await fetch(url+endpoint);assert.equal(response.status,200,endpoint);await response.text()
  }
  for(const endpoint of ['/scripts','/scripts/install.sh','/api/scripts','/api/scripts/install.sh/raw']) {
   response=await fetch(url+endpoint,{headers:{'X-Fixture-Reject':'1'}});assert.equal(response.status,200,endpoint);await response.text()
  }
  response=await fetch(url+'/api/status',{headers:{'X-Fixture-Reject':'1'}});assert.equal(response.status,403);await response.text()
  for(const endpoint of ['/api/scripts/repository','/api/scripts/repository/pull','/api/status']) {
   response=await fetch(url+endpoint);assert.equal(response.status,503,endpoint);await response.text()
  }
  for(const endpoint of ['/v1/chat/completions','/v1/systemone','/typesafe/v1/systemone']) {
   response=await fetch(url+endpoint,{method:'POST',headers:{Accept:'text/html','Content-Type':'application/json'},body:'{}'})
   assert.equal(response.status,503,endpoint);assert.match(response.headers.get('content-type'),/application\/json/);assert.equal((await response.json()).error.code,'service_temporarily_unavailable')
   response=await fetch(url+endpoint,{method:'POST',headers:{Accept:'application/json','X-Fixture-Allow':'1','Content-Type':'application/json'},body:'{}'})
   assert.equal(response.status,500,endpoint);assert.deepEqual(await response.json(),{error:{message:'upstream test failure'}})
   response=await fetch(url+endpoint,{method:'POST',headers:{'X-Fixture-Reject':'1','Content-Type':'application/json'},body:'{}'})
   assert.equal(response.status,403,endpoint);await response.text()
  }
  for(const endpoint of ['/.well-known/oauth-authorization-server','/.well-known/oauth-protected-resource/api/oauth2']) {
   response=await fetch(url+endpoint,{headers:{'X-Fixture-Allow':'1'}});assert.equal(response.status,200);assert.match(response.headers.get('content-type'),/application\/json/);assert.equal((await response.json()).issuer,'https://api.lmm.best')
   response=await fetch(url+endpoint);assert.equal(response.status,503)
  }
  for(const endpoint of ['/api/oauth2/authorize','/api/user/auth/oauth2/consent','/oauth/fixture']) {
   response=await fetch(url+endpoint+'?state=oauth-secret-fixture',{headers:{'X-Fixture-Allow':'1'}});await response.text()
  }
  response=await fetch(url+'/api/status?ordinary=retained',{headers:{'X-Fixture-Allow':'1'}});await response.text()
  response=await fetch(url+'/api/status',{headers:{'X-Fixture-Allow':'1',Referer:'https://api.lmm.best/oauth/fixture?code=oauth-secret-referrer'}});await response.text()
  response=await fetch(url+'/api/oauth2/authorize?state=oauth-secret-options',{method:'OPTIONS'});assert.equal(response.status,204)
  const accessLog=fs.readFileSync(path.join(dir,'access.log'),'utf8');assert.match(accessLog,/ordinary=retained/);assert.doesNotMatch(accessLog,/oauth-secret-/)
  response=await fetch(url+'/v1/chat/completions',{method:'OPTIONS'});assert.equal(response.status,204)
  response=await fetch(url+'/internal/errors/service-unavailable');assert.equal(response.status,404)
  response=await fetch(url+'/__lmm_service_status');assert.equal(response.status,200);assert.equal((await response.json()).state,'maintenance')
 } finally {
  if(process && process.exitCode===null){process.kill('SIGTERM');await once(process,'exit')}
  await new Promise(resolve=>backend.close(resolve));fs.rmSync(dir,{recursive:true,force:true})
 }
})

test('TLS public script admission survives denied status policy and remains behind the maintenance barrier', async () => {
 const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'lmm-nginx-restored-tls-'))
 const templates = path.join(root, 'packaging/common/lmm-api/edge-policy/nginx')
 const requests = []
 let scripts = []
 const backend = http.createServer((req, res) => {
  requests.push({ url: req.url, headers: req.headers })
  if (req.url === '/internal/access-ip-policy') {
   res.writeHead(403, { 'X-LMM-Access-Policy': 'denied' }).end()
  } else if (req.url === '/internal/errors/access-policy') {
   assert.equal(req.headers['x-lmm-internal-error'], 'access-policy')
   assert.equal(req.headers['x-lmm-access-policy'], 'denied')
   res.writeHead(404, { 'Content-Type': 'application/json' }).end('{"message":"Not Found"}')
  } else if (req.url === '/api/scripts') {
   res.writeHead(200, { 'Content-Type': 'application/json' }).end(JSON.stringify({ success: true, data: scripts }))
  } else if (req.url === '/api/status') {
   res.writeHead(200, { 'Content-Type': 'application/json' }).end('{"success":true,"data":{"version":"0.2.111"}}')
  } else {
   res.writeHead(404).end()
  }
 })
 let nginx
 try {
  execFileSync('openssl', ['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1', '-subj', '/CN=LMM fixture CA', '-addext', 'basicConstraints=critical,CA:TRUE', '-keyout', `${dir}/ca.key`, '-out', `${dir}/ca.crt`], { stdio: 'pipe' })
  execFileSync('openssl', ['req', '-new', '-newkey', 'rsa:2048', '-nodes', '-subj', '/CN=api.lmm.best', '-keyout', `${dir}/tls.key`, '-out', `${dir}/tls.csr`], { stdio: 'pipe' })
  fs.writeFileSync(`${dir}/tls.ext`, 'subjectAltName=DNS:api.lmm.best\nextendedKeyUsage=serverAuth\nkeyUsage=digitalSignature,keyEncipherment\n')
  execFileSync('openssl', ['x509', '-req', '-in', `${dir}/tls.csr`, '-CA', `${dir}/ca.crt`, '-CAkey', `${dir}/ca.key`, '-CAcreateserial', '-days', '1', '-extfile', `${dir}/tls.ext`, '-out', `${dir}/tls.crt`], { stdio: 'pipe' })
  backend.listen(0, '127.0.0.1');await once(backend, 'listening')
  const reservation = net.createServer();reservation.listen(0, '127.0.0.1');await once(reservation, 'listening')
  const port = reservation.address().port;await new Promise(resolve => reservation.close(resolve))
  const source = fs.readFileSync(`${templates}/lmm-api-locations.conf`, 'utf8')
  fs.writeFileSync(`${dir}/locations.conf`, source.replaceAll('/var/log/nginx/access.log', `${dir}/access.log`).replaceAll('/etc/nginx/lmm-api-mime.types', `${templates}/mime.types`).replaceAll('/srv/lmm-api-frontend', `${dir}/frontend`))
  const maps = fs.readFileSync(`${templates}/http-map.conf`, 'utf8')
   .replace(/geoip2[^]*?\n\}/, '')
   .replaceAll('127.0.0.1:3000', `127.0.0.1:${backend.address().port}`)
   .replace('include /etc/lmm-api/nginx/peers/*.conf;', '')
  const tlsRequest = route => new Promise((resolve, reject) => {
   const child = spawn('curl', ['--disable', '--silent', '--show-error', '--noproxy', '*', '--max-time', '2', '--cacert', `${dir}/ca.crt`, '--resolve', `api.lmm.best:${port}:127.0.0.1`, '--write-out', '\n%{http_code}', `https://api.lmm.best:${port}${route}`])
   let stdout = '', stderr = ''
   child.stdout.on('data', chunk => { stdout += chunk })
   child.stderr.on('data', chunk => { stderr += chunk })
   child.on('error', reject)
   child.on('close', code => {
    if (code !== 0) { reject(new Error(`TLS curl failed (${code}): ${stderr}`));return }
    const boundary = stdout.lastIndexOf('\n')
    resolve({ status: Number(stdout.slice(boundary + 1)), body: stdout.slice(0, boundary) })
   })
  })
  const stop = async () => {
   if (nginx && nginx.exitCode === null) { nginx.kill('SIGTERM');await once(nginx, 'exit') }
  }
  const start = async barrier => {
   await stop()
   fs.writeFileSync(`${dir}/nginx.conf`, `worker_processes 1;
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
 map $host $lmm_geoip_country_code {default "";}
 ${maps}
 server {
  listen 127.0.0.1:${port} ssl;
  server_name api.lmm.best;
  ssl_certificate ${dir}/tls.crt;
  ssl_certificate_key ${dir}/tls.key;
  ${barrier ? "return 503 'lmm-schema-maintenance:fixture';" : ''}
  include ${templates}/lmm-api-region-policy.conf;
  include ${dir}/locations.conf;
 }
}`)
   execFileSync('nginx', ['-t', '-p', dir, '-c', `${dir}/nginx.conf`], { stdio: 'pipe' })
   nginx = spawn('nginx', ['-p', dir, '-c', `${dir}/nginx.conf`, '-g', 'daemon off; master_process off;'], { stdio: 'ignore' })
   for (let attempt = 0; attempt < 50; attempt++) {
    try { return await tlsRequest('/api/scripts') } catch (error) {
     if (attempt === 49) throw error
     await new Promise(resolve => setTimeout(resolve, 30))
    }
   }
  }
  // Local process version and TLS admission are independent observations.
  const localStatus = await fetch(`http://127.0.0.1:${backend.address().port}/api/status`)
  assert.equal(localStatus.status, 200)
  assert.deepEqual(await localStatus.json(), { success: true, data: { version: '0.2.111' } })
  let response = await start(false)
  assert.equal(response.status, 200)
  assert.deepEqual(JSON.parse(response.body), { success: true, data: [] })
  response = await tlsRequest('/api/status')
  assert.equal(response.status, 404)
  assert.deepEqual(JSON.parse(response.body), { message: 'Not Found' })
  assert.equal(requests.filter(request => request.url === '/api/status').length, 1, 'denied TLS status never reaches the status handler')
  const policyRequest = requests.find(request => request.url === '/internal/access-ip-policy')
  assert.equal(policyRequest.headers['x-original-client-ip'], '127.0.0.1')
  assert.equal(policyRequest.headers['x-lmm-edge-country'], undefined)
  scripts = [{ name: 'install.sh', size: 17, updated: '2026-10-12T00:00:00Z', fetches: 0 }]
  const policyAttempts = requests.filter(request => request.url === '/internal/access-ip-policy').length
  response = await tlsRequest('/api/scripts')
  assert.equal(response.status, 200)
  assert.deepEqual(JSON.parse(response.body), { success: true, data: scripts })
  assert.equal(requests.filter(request => request.url === '/internal/access-ip-policy').length, policyAttempts, 'script named redirect retains its existing auth exemption')
  const beforeBarrier = requests.length
  response = await start(true)
  assert.equal(response.status, 503)
  assert.equal(response.body, 'lmm-schema-maintenance:fixture')
  assert.equal((await tlsRequest('/api/status')).status, 503)
  assert.equal(requests.length, beforeBarrier, 'server barrier executes before public routing and auth_request')
  response = await start(false)
  assert.equal(response.status, 200)
  assert.deepEqual(JSON.parse(response.body), { success: true, data: scripts })
  assert.equal((await tlsRequest('/api/status')).status, 404, 'restoration retains the original access policy')
 } finally {
  if (nginx && nginx.exitCode === null) { nginx.kill('SIGTERM');await once(nginx, 'exit') }
  backend.closeAllConnections()
  if (backend.listening) await new Promise(resolve => backend.close(resolve))
  fs.rmSync(dir, { recursive: true, force: true })
 }
})
