/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import vm from 'node:vm'
import { test } from 'node:test'
import { execFileSync } from 'node:child_process'
import { Window } from 'happy-dom'

const root = path.resolve(import.meta.dirname, '..')
const html = fs.readFileSync(path.join(root, 'packaging/common/lmm-api/edge-policy/service-unavailable.html'), 'utf8')
const script = html.match(/<script>([\s\S]*?)<\/script>/)[1]
async function render(data, language = 'en', live = false) {
 const window = new Window({ settings: { disableJavaScriptEvaluation: true } })
 window.document.write(html)
 vm.runInNewContext(script, {
  document: window.document, navigator: { language }, location: { reload() {} },
  Date, Intl, AbortSignal, setInterval() {},
  fetch: async (url) => ({ ok: true, json: async () => url === '/api/livez' ? { live } : data }),
 })
 await new Promise(resolve => setImmediate(resolve))
 return window
}

test('checked-in nginx document matches its readable source', () => {
 execFileSync(process.execPath, [path.join(root, 'scripts/generate-nginx-error-page.mjs'), '--check'])
})
test('missing or stale status never invents maintenance or a recovery time', async () => {
 for (const data of [{}, { state: 'maintenance', updated_at: '2000-01-01T00:00:00Z' }]) {
  const window = await render(data)
  assert.equal(window.document.getElementById('eta').textContent, 'Recovery time has not been confirmed')
  assert.match(window.document.getElementById('reason').textContent, /cause is being checked/)
  await window.close()
 }
})
test('public explanations and model names are text, not executable HTML', async () => {
 const message = '<img src=x onerror=alert(1)>'
 const window = await render({ state:'maintenance', updated_at:new Date().toISOString(), service:message, message })
 assert.equal(window.document.getElementById('reason').textContent, message)
 assert.equal(window.document.getElementById('scope').textContent, message)
 assert.equal(window.document.querySelector('img'), null)
 await window.close()
})
test('only a valid future estimate is shown; an expired estimate is explicit', async () => {
 for (const seconds of [3600, -60]) {
  const window = await render({ state:'maintenance', updated_at:new Date().toISOString(), estimated_recovery_at:new Date(Date.now()+seconds*1000).toISOString() })
  const eta = window.document.getElementById('eta').textContent
  if(seconds<0) assert.match(eta, /estimate has passed/)
  else { assert.notEqual(eta,'Recovery time has not been confirmed'); assert.ok(eta.length>5) }
  await window.close()
 }
})
test('ready status requires a live backend before announcing recovery', async () => {
 for (const live of [false,true]) {
  const window = await render({ state:'ready', updated_at:new Date().toISOString() },'en',live)
  assert.equal(window.document.getElementById('intro').textContent.includes('available again'),live)
  await window.close()
 }
})
test('all seven supported languages render recovery guidance', async () => {
 for(const language of ['en','zh','zh-TW','fr','ja','ru','vi']){
  const window=await render({state:'recovering',updated_at:new Date().toISOString()},language)
  assert.equal(window.document.documentElement.lang,language)
  assert.ok(window.document.getElementById('reason').textContent.length>5)
  await window.close()
 }
})
