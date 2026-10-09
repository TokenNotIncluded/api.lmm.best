import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE)
const origin = 'http://127.0.0.1:4174'
const output = process.env.L0_REVIEW_OUTPUT
assert.ok(output)
await mkdir(output, { recursive: true })
const report = []
const browser = await chromium.launch({ headless: true })
try {
  for (const theme of ['dark', 'light']) {
    for (const [width, height] of [[320, 720], [390, 844], [430, 932], [1440, 1000]]) {
      const context = await browser.newContext({
        viewport: { width, height }, locale: 'zh-CN', colorScheme: theme,
        serviceWorkers: 'block', isMobile: width < 600, hasTouch: width < 600,
      })
      await context.addCookies([{ name: 'vite-ui-theme', value: theme, url: origin }])
      await context.addInitScript(() => {
        localStorage.setItem('i18nextLng', 'zhCN')
        localStorage.setItem('lmm:source-consent:v2', 'no')
      })
      const errors = []
      await context.route('**/*', async route => {
        const url = new URL(route.request().url())
        if (url.origin !== origin) return route.abort('blockedbyclient')
        if (url.pathname === '/api/status') {
          return route.fulfill({ json: { success: true, data: {
            system_name: 'LMM Forge', logo: '/logo.png',
            assistant: { enabled: true }, announcements_enabled: false,
          } } })
        }
        if (url.pathname.startsWith('/api/')) {
          errors.push(`Unmocked network request: ${route.request().method()} ${url.pathname}`)
          return route.abort('blockedbyclient')
        }
        return route.continue()
      })
      const page = await context.newPage()
      page.setDefaultTimeout(15000)
      page.on('pageerror', error => errors.push(String(error)))
      const entry = { theme, width, height, errors }
      try {
        await page.goto(`${origin}/getting-started?debug_persona=l0&console_review=1`, { waitUntil: 'domcontentloaded', timeout: 60000 })
        await page.locator('#l0-upgrade-title').waitFor({ timeout: 60000 })
        await page.evaluate(() => document.fonts.ready)
        await page.waitForTimeout(1000)
        assert.equal(await page.locator('#l0-upgrade-title').innerText(), '升级为正式用户')
        assert.equal(await page.getByTestId('l0-upgrade-description').innerText(), '通过对话（免费）或充值，升级为正式用户。')
        assert.equal(await page.locator('.l0-topbar').count(), 0)
        entry.geometry = await page.evaluate(() => {
          const rect = selector => {
            const e = document.querySelector(selector)
            if (!e) throw Error(`Missing ${selector}`)
            const r = e.getBoundingClientRect()
            return { x: r.x, y: r.y, width: r.width, height: r.height, right: r.right, bottom: r.bottom, scrollWidth: e.scrollWidth, clientWidth: e.clientWidth }
          }
          return {
            viewport: innerWidth, documentWidth: document.documentElement.scrollWidth,
            chat: rect('[data-testid="l0-chat-free"]'), topup: rect('[data-testid="l0-topup-direct"]'),
            input: rect('#l0-question'), content: rect('.console-section-content'),
            inputFont: getComputedStyle(document.querySelector('#l0-question')).fontSize,
            railBorder: getComputedStyle(document.querySelector('.l0-rail')).borderTopWidth,
          }
        })
        const g = entry.geometry
        assert.ok(g.documentWidth <= width + 1, 'document must not overflow horizontally')
        assert.ok(g.content.scrollWidth <= g.content.clientWidth + 1, 'content must not overflow horizontally')
        for (const b of [g.chat, g.topup]) {
          assert.ok(b.height >= 44 && b.width >= 44, 'comfortable touch target')
          assert.ok(b.x >= 0 && b.right <= width + 1, 'action stays within viewport')
          assert.ok(b.scrollWidth <= b.clientWidth + 1, 'button label must not overflow')
        }
        assert.ok(g.chat.right <= g.topup.x + 1, 'primary controls do not overlap')
        assert.equal(g.railBorder, '0px')
        assert.equal(g.inputFont, '16px')
        if (width === 390 || width === 430) assert.ok(g.input.bottom <= g.content.bottom, 'composer is visible in the first mobile screen')
        await page.screenshot({ path: path.join(output, `${theme}-${width}.png`), fullPage: true })
        await page.getByTestId('l0-chat-free').click()
        await page.waitForFunction(() => document.activeElement?.id === 'l0-question')
        await page.locator('#l0-question').fill('保留这条尚未发送的问题')
        await page.locator('#l0-tab-explore').click()
        await page.getByTestId('l0-chat-free').click()
        assert.equal(await page.locator('#l0-question').inputValue(), '保留这条尚未发送的问题')
        await page.locator('#l0-tab-access').click()
        assert.equal(await page.getByTestId('l0-account-details').getAttribute('open'), '')
        assert.ok(await page.getByTestId('l0-paid-progress').isVisible())
        assert.equal(await page.getByTestId('l0-contact-support').getAttribute('href'), '/support')
        await page.locator('#l0-tab-access').focus()
        await page.keyboard.press('Escape')
        assert.equal(await page.locator('#l0-panel-chat').getAttribute('hidden'), null)
        if (width === 390) {
          await page.emulateMedia({ reducedMotion: 'reduce' })
          assert.equal(await page.getByTestId('l0-chat-free').evaluate(e => getComputedStyle(e).transitionDuration), '0s')
          await page.getByTestId('l0-topup-direct').click()
          await page.waitForURL(/\/wallet/)
        }
        assert.deepEqual(errors, [])
        entry.ok = true
      } catch (error) {
        entry.ok = false
        entry.failure = String(error.stack ?? error)
        await page.screenshot({ path: path.join(output, `failure-${theme}-${width}.png`), fullPage: true }).catch(() => {})
        entry.text = await page.locator('body').innerText().catch(() => '')
      } finally {
        report.push(entry)
        console.log(JSON.stringify(entry))
        await writeFile(path.join(output, 'report.json'), JSON.stringify(report, null, 2))
        await context.close()
      }
    }
  }
} finally {
  await browser.close()
}
assert.ok(report.every(entry => entry.ok), 'all viewport and interaction checks must pass')
