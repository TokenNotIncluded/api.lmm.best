/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
// Exercise the production bundle against local, read-only fictional data.
import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ?? 'playwright')
const origin = process.env.HOME_REVIEW_ORIGIN ?? 'http://127.0.0.1:4175'
const output = process.env.HOME_REVIEW_OUTPUT ?? 'artifacts/home-review'
assert.ok(
  ['127.0.0.1', 'localhost', '[::1]'].includes(new URL(origin).hostname)
)
await mkdir(output, { recursive: true })
const links = [
  ['atlas', 'Atlas Chat', 'chat'],
  ['lumen', 'Lumen Chat', 'chat'],
  ['prism', 'Prism Chat', 'chat'],
  ['papers', 'Research Library', 'research'],
  ['studio', 'Creative Studio', 'creative'],
  ['forge', 'Developer Workshop', 'developer'],
].map(([id, name, category]) => ({
  id,
  name,
  category,
  url: `https://${id}.example/`,
  summary: 'A sample website for checking the directory layout.',
  description: 'This fictional entry is used only in browser tests.',
  enabled: true,
}))
const browser = await chromium.launch({ headless: true })
const results = []
try {
  for (const [name, width, theme, motion] of [
    ['desktop', 1440, 'light', 'no-preference'],
    ['dark', 1440, 'dark', 'no-preference'],
    ['mobile', 390, 'light', 'no-preference'],
    ['compact', 320, 'dark', 'no-preference'],
    ['reduced', 390, 'light', 'reduce'],
  ]) {
    const mobile = width < 768
    const context = await browser.newContext({
      viewport: { width, height: 900 },
      isMobile: mobile,
      hasTouch: mobile,
      reducedMotion: motion,
      serviceWorkers: 'block',
      locale: 'zh-CN',
    })
    await context.addCookies([
      { name: 'vite-ui-theme', value: theme, url: origin },
    ])
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
      if (url.pathname === '/api/status') {
        data = {
          system_name: 'LMM',
          register_enabled: true,
          assistant: { enabled: false },
          backend_capabilities: { bounty_public_read: false },
        }
      }
      if (url.pathname === '/api/notice') data = ''
      if (url.pathname === '/api/ai-directory') data = { links }
      if (url.pathname === '/api/ai-directory/ads') {
        data = { items: [], has_more: false, next_offset: 0 }
      }
      return route.fulfill({ json: { success: true, data } })
    })
    const page = await context.newPage()
    const errors = []
    page.on('pageerror', (error) => errors.push(error.message))
    try {
      await page.goto(origin, { waitUntil: 'networkidle' })
      await page.locator('#lmm-home-title').waitFor()
      const entry = page.locator('.home-directory-link')
      const progress = entry.getByRole('progressbar')
      await progress.waitFor()
      assert.equal(await page.locator('.lmm-poster-caption').count(), 0)
      assert.equal(await progress.getAttribute('aria-valuenow'), '0')
      await page.screenshot({ path: `${output}/directory-entry-${name}.png` })
      if (mobile) {
        const box = await entry.boundingBox()
        assert.ok(box && box.y >= 0)
        const session = await context.newCDPSession(page)
        const x = Math.round(box.x + box.width / 2)
        const startY = Math.round(box.y + box.height - 14)
        assert.ok(startY > 84, 'the full swipe must fit inside the screen')
        const touch = (type, y) =>
          session.send('Input.dispatchTouchEvent', {
            type,
            touchPoints: y === undefined ? [] : [{ x, y, id: 0 }],
          })
        // A short swipe must not turn into a link click or a route change.
        await touch('touchStart', startY)
        await touch('touchMove', startY - 24)
        await touch('touchEnd')
        await page.waitForTimeout(100)
        assert.equal(new URL(page.url()).pathname, '/')
        assert.equal(await progress.getAttribute('aria-valuenow'), '0')
        await touch('touchStart', startY)
        await touch('touchMove', startY - 36)
        assert.ok(Number(await progress.getAttribute('aria-valuenow')) > 0)
        await touch('touchMove', startY - 80)
        assert.equal(await progress.getAttribute('aria-valuenow'), '100')
        assert.equal(new URL(page.url()).pathname, '/')
        await touch('touchEnd')
        await session.detach()
      } else {
        await page.mouse.move(width / 2, 350)
        await page.mouse.wheel(0, -80)
        await page.waitForTimeout(40)
        assert.ok(Number(await progress.getAttribute('aria-valuenow')) > 0)
        await page.mouse.wheel(0, 20)
        await page.waitForTimeout(40)
        assert.equal(new URL(page.url()).pathname, '/')
        assert.equal(await progress.getAttribute('aria-valuenow'), '0')
        await page.evaluate(() => window.scrollTo(0, 0))
        for (let i = 0; i < 3; i++) {
          await page.mouse.wheel(0, -80)
          await page.waitForTimeout(40)
        }
      }
      await page.waitForURL('**/ai-directory')
      await page.waitForFunction(
        () => document.querySelectorAll('.ai-directory-item').length === 6
      )
      await page.evaluate(() => document.fonts.ready)
      assert.equal(
        await page.evaluate(() => document.documentElement.scrollWidth),
        width,
        `${name}: directory horizontal overflow`
      )
      await page.screenshot({
        path: `${output}/directory-page-${name}.png`,
        fullPage: true,
      })
      const search = page.locator('.ai-directory-search input')
      await search.fill('Atlas')
      await page.waitForFunction(
        () => document.querySelectorAll('.ai-directory-item').length === 1
      )
      const card = page.locator('.ai-directory-item').first()
      assert.equal(await card.locator('a').getAttribute('href'), links[0].url)
      assert.equal(await card.locator('a').getAttribute('target'), '_blank')
      assert.match(await card.locator('a').getAttribute('rel'), /noopener/)
      await card.locator('summary').click()
      assert.ok(await card.locator('details').evaluate((node) => node.open))
      await search.fill('No such sample website')
      await page.waitForFunction(
        () => document.querySelectorAll('.ai-directory-item').length === 0
      )
      await page.locator('.ai-directory-state button').click()
      await page.waitForFunction(
        () => document.querySelectorAll('.ai-directory-item').length === 6
      )
      await page.locator('.ai-directory-filters button').nth(1).click()
      await page.waitForFunction(
        () => document.querySelectorAll('.ai-directory-item').length === 3
      )
      assert.deepEqual(errors, [], `${name}: browser exceptions`)
      results.push({ name, width, theme, motion, passed: true })
    } finally {
      await context.close()
    }
  }
} finally {
  await writeFile(
    `${output}/directory-results.json`,
    JSON.stringify(results, null, 2)
  )
  await browser.close()
}
console.log(JSON.stringify(results, null, 2))
