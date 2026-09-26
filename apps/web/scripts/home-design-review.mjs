/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
// Render the real production bundle with fictional, read-only API fixtures.
import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ?? 'playwright')
const origin = process.env.HOME_REVIEW_ORIGIN ?? 'http://127.0.0.1:4175'
const output = process.env.HOME_REVIEW_OUTPUT ?? 'artifacts/home-review'
assert.ok(['127.0.0.1', 'localhost', '[::1]'].includes(new URL(origin).hostname))
await mkdir(output, { recursive: true })
const browser = await chromium.launch({ headless: true })
const results = []
try {
  for (const [name, width, height, theme, motion] of [
    ['desktop', 1440, 1000, 'light', 'no-preference'],
    ['mobile', 390, 844, 'light', 'no-preference'],
    ['compact', 320, 740, 'light', 'no-preference'],
    ['dark', 1440, 1000, 'dark', 'no-preference'],
    ['reduced', 390, 844, 'light', 'reduce'],
  ]) {
    const context = await browser.newContext({
      viewport: { width, height }, locale: 'zh-CN', reducedMotion: motion,
      serviceWorkers: 'block',
    })
    await context.addCookies([{ name: 'vite-ui-theme', value: theme, url: origin }])
    await context.addInitScript(() => {
      localStorage.setItem('i18nextLng', 'zhCN')
      localStorage.setItem('lmm:source-consent:v2', 'no')
    })
    await context.route('**/*', (route) => {
      const url = new URL(route.request().url())
      if (url.origin !== origin) return route.abort('blockedbyclient')
      if (!url.pathname.startsWith('/api/')) return route.continue()
      if (url.pathname === '/api/user/auth/refresh') {
        return route.fulfill({ status: 401, json: { success: false } })
      }
      let data = []
      if (url.pathname === '/api/setup') data = { status: true }
      if (url.pathname === '/api/status') data = {
        system_name: 'LMM', register_enabled: true,
        assistant: { enabled: false },
        backend_capabilities: { bounty_public_read: false },
      }
      if (url.pathname === '/api/notice') data = ''
      return route.fulfill({ json: { success: true, data } })
    })
    const page = await context.newPage()
    const errors = []
    page.on('pageerror', (error) => errors.push(error.message))
    await page.goto(origin, { waitUntil: 'networkidle' })
    await page.locator('#lmm-home-title').waitFor()
    await page.waitForFunction(() => document.querySelector('.lmm-home')?.dataset.motion !== 'loading')
    await page.waitForTimeout(1200)
    await page.screenshot({ path: `${output}/${name}.png` })
    const metrics = await page.evaluate(() => {
      const box = (selector) => {
        const rect = document.querySelector(selector)?.getBoundingClientRect()
        return rect && { x: rect.x, y: rect.y, width: rect.width, height: rect.height }
      }
      return {
        scrollWidth: document.documentElement.scrollWidth,
        motion: document.querySelector('.lmm-home')?.dataset.motion,
        title: box('#lmm-home-title'), visual: box('[data-home-visual]'),
        controls: box('.lmm-core-steps'),
        buttons: Array.from(document.querySelectorAll('[data-cinema-jump]')).map((button) => ({
          text: button.textContent, width: button.getBoundingClientRect().width,
          height: button.getBoundingClientRect().height,
        })),
      }
    })
    assert.equal(metrics.scrollWidth, width, `${name}: horizontal overflow`)
    assert.deepEqual(errors, [], `${name}: browser exceptions`)
    if (motion === 'no-preference') {
      const toggle = page.locator('[data-motion-toggle]')
      await toggle.click()
      assert.equal(await toggle.getAttribute('aria-pressed'), 'true')
      await toggle.click()
      assert.equal(await toggle.getAttribute('aria-pressed'), 'false')
      await page.locator('[data-cinema-jump="1"]').click()
      await page.waitForFunction(() => document.querySelector('[data-cinema-panel="1"]')?.hasAttribute('data-active'))
      await page.screenshot({ path: `${output}/${name}-api.png` })
      await page.locator('[data-cinema-jump="0"]').click()
    }
    results.push({ name, width, height, theme, ...metrics, errors })
    await context.close()
  }
  await writeFile(`${output}/results.json`, JSON.stringify(results, null, 2))
  console.log(JSON.stringify({ result: 'PASS', results }))
} finally {
  await browser.close()
}
