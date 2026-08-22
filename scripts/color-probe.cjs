// Probe saturated (blue) colors on a page.
const EDGE = 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe'
const PORT = Number(process.env.PROBE_PORT || 9441)
const URL = process.argv[2] || 'http://127.0.0.1:3000/sign-in'

const { spawn } = require('node:child_process')
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

async function main() {
  const proc = spawn(EDGE, [
    `--remote-debugging-port=${PORT}`, '--headless=new', '--disable-gpu', '--no-first-run',
    '--user-data-dir=D:/Development/LMM-Forge/.edge-color-probe', '--window-size=1600,900', 'about:blank',
  ], { stdio: 'ignore' })
  let t = null
  for (let i = 0; i < 40; i++) {
    try { const r = await fetch(`http://127.0.0.1:${PORT}/json/list`); t = await r.json(); if (t && t.length) break } catch {}
    await sleep(500)
  }
  const page = t.find((x) => x.type === 'page')
  const ws = new WebSocket(page.webSocketDebuggerUrl)
  await new Promise((r) => { ws.onopen = r })
  let id = 0; const pend = new Map()
  ws.onmessage = (ev) => { const m = JSON.parse(ev.data); if (m.id && pend.has(m.id)) { pend.get(m.id)(m); pend.delete(m.id) } }
  const send = (m, p = {}) => new Promise((r) => { const i = ++id; pend.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
  const evl = async (e) => {
    const r = await send('Runtime.evaluate', { awaitPromise: true, returnByValue: true, expression: e })
    if (r.result && r.result.exceptionDetails) {
      return 'EXC:' + ((r.result.exceptionDetails.exception && r.result.exceptionDetails.exception.description) || r.result.exceptionDetails.text || '').slice(0, 200)
    }
    return r.result && r.result.result ? r.result.result.value : 'ERR'
  }
  await send('Page.enable'); await send('Runtime.enable')
  await send('Page.navigate', { url: URL })
  await sleep(12000)

  const script = `(() => {
    const out = []
    document.querySelectorAll('button, a, input, svg, [class]').forEach((el) => {
      const c = getComputedStyle(el)
      const props = [c.backgroundColor, c.color, c.borderColor]
      for (const v of props) {
        if (!v) continue
        const mm = v.match(/rgba?\\((\\d+), (\\d+), (\\d+)/)
        if (mm) {
          const r2 = +mm[1], g2 = +mm[2], b2 = +mm[3]
          if (b2 - r2 > 30 && b2 - g2 > 30) {
            const cls = el.className && typeof el.className === 'string' ? el.className.split(' ').slice(0, 3).join('.') : ''
            out.push(el.tagName + (cls ? '.' + cls : '') + '=' + v.slice(0, 22))
            break
          }
        }
      }
    })
    return [...new Set(out)].slice(0, 10).join(' ;; ') || 'NO-BLUE'
  })()`
  console.log(URL, '=>')
  console.log(await evl(script))
  ws.close(); proc.kill(); process.exit(0)
}
main().catch((e) => { console.error(e); process.exit(1) })
