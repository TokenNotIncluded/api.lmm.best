import assert from 'node:assert/strict'
import { writeFile, mkdir } from 'node:fs/promises'
import { setTimeout as delay } from 'node:timers/promises'

const origin = 'https://api.lmm.best'
const output = process.env.BROWSER_EVIDENCE_DIR || '/tmp/public-browser-evidence'
await mkdir(output, { recursive: true })
const targets = await fetch('http://127.0.0.1:9222/json/list').then(r => r.json())
const target = targets.find(t => t.type === 'page')
assert.ok(target, 'Chrome must expose an isolated page target')
const ws = new WebSocket(target.webSocketDebuggerUrl)
await new Promise((resolve, reject) => {
  ws.addEventListener('open', resolve, { once: true })
  ws.addEventListener('error', reject, { once: true })
})
let serial = 0
const pending = new Map()
let exceptions = []
let failedAssets = []
ws.addEventListener('message', ({ data }) => {
  const message = JSON.parse(data)
  if (message.id) {
    const call = pending.get(message.id)
    if (!call) return
    pending.delete(message.id)
    clearTimeout(call.timer)
    if (message.error) call.reject(new Error(message.error.message))
    else call.resolve(message.result)
  } else if (message.method === 'Runtime.exceptionThrown') {
    const detail = message.params.exceptionDetails
    exceptions.push({ type: detail.exception?.className || 'Error', text: detail.text })
  } else if (message.method === 'Network.responseReceived') {
    const { response, type } = message.params
    const url = new URL(response.url)
    if (url.origin === origin && ['Script', 'Stylesheet'].includes(type) && response.status >= 400) {
      failedAssets.push({ path: url.pathname, status: response.status })
    }
  }
})
function command(method, params = {}) {
  return new Promise((resolve, reject) => {
    const id = ++serial
    const timer = setTimeout(() => {
      pending.delete(id)
      reject(new Error(`DevTools timeout: ${method}`))
    }, 20000)
    pending.set(id, { resolve, reject, timer })
    ws.send(JSON.stringify({ id, method, params }))
  })
}
async function evaluate(expression) {
  const result = await command('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true })
  assert.ok(!result.exceptionDetails, 'Page evaluation failed')
  return result.result.value
}
const results = []
try {
  await command('Page.enable')
  await command('Runtime.enable')
  await command('Network.enable')
  await command('Network.setCacheDisabled', { cacheDisabled: true })
  for (const view of [
    { name: 'home-desktop', path: '/', width: 1440, height: 1000, dark: false },
    { name: 'home-mobile', path: '/', width: 390, height: 844, dark: true },
    { name: 'login-desktop', path: '/login', width: 1440, height: 1000, dark: false },
    { name: 'console-anonymous', path: '/console', width: 390, height: 844, dark: false },
  ]) {
    exceptions = []
    failedAssets = []
    await command('Emulation.setDeviceMetricsOverride', { width: view.width, height: view.height, deviceScaleFactor: 1, mobile: view.width < 600 })
    await command('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: view.dark ? 'dark' : 'light' }] })
    const navigation = await command('Page.navigate', { url: origin + view.path })
    assert.ok(!navigation.errorText, 'Public navigation failed')
    let rendered = false
    for (let attempt = 0; attempt < 90; attempt++) {
      await delay(500)
      rendered = await evaluate("document.readyState === 'complete' && !!document.querySelector('#root')?.children.length && document.body.innerText.trim().length > 80")
      if (rendered) break
    }
    await delay(2500)
    const measurements = await evaluate(`({title:document.title,width:innerWidth,scrollWidth:document.documentElement.scrollWidth,rootChildren:document.querySelector('#root')?.children.length ?? 0,textLength:document.body.innerText.trim().length,path:location.pathname,links:document.querySelectorAll('a[href]').length})`)
    const screenshot = await command('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false })
    await writeFile(`${output}/${view.name}.png`, Buffer.from(screenshot.data, 'base64'))
    results.push({ ...view, rendered, measurements, exceptions: [...exceptions], failedAssets: [...failedAssets] })
  }
  await writeFile(`${output}/browser.json`, JSON.stringify({ origin, checkedAt: new Date().toISOString(), results }, null, 2))
  for (const result of results) {
    assert.equal(result.rendered, true, `${result.name}: application did not render`)
    assert.equal(result.exceptions.length, 0, `${result.name}: unhandled JavaScript errors`)
    assert.equal(result.failedAssets.length, 0, `${result.name}: missing application assets`)
    assert.ok(result.measurements.scrollWidth <= result.width + 1, `${result.name}: horizontal overflow`)
  }
  console.log(JSON.stringify({ views: results.length, exceptions: results.reduce((n,r) => n+r.exceptions.length,0), passed: true }))
} finally {
  await writeFile(`${output}/browser.json`, JSON.stringify({ origin, checkedAt: new Date().toISOString(), results }, null, 2))
  ws.close()
}
