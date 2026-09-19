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
 const backend = http.createServer((req,res) => {if(req.url.startsWith('/.well-known/oauth-')){res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({issuer:'https://api.lmm.best'}));return;}res.writeHead(500,{'Content-Type':'application/json'});res.end('{"error":{"message":"upstream test failure"}}')})
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
  location = /__test_auth {internal;auth_request off;if ($http_x_fixture_allow = 1) {return 204;}return 500;}
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
  response=await fetch(url+'/v1/chat/completions',{method:'POST',headers:{Accept:'text/html','Content-Type':'application/json'},body:'{}'})
  assert.equal(response.status,503);assert.match(response.headers.get('content-type'),/application\/json/);assert.equal((await response.json()).error.code,'service_temporarily_unavailable')
  response=await fetch(url+'/v1/chat/completions',{method:'POST',headers:{Accept:'application/json','X-Fixture-Allow':'1','Content-Type':'application/json'},body:'{}'})
  assert.equal(response.status,500);assert.deepEqual(await response.json(),{error:{message:'upstream test failure'}})
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
