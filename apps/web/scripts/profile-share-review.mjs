/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import path from 'node:path'

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ?? 'playwright')
const origin = 'http://127.0.0.1:4174'
const output = process.env.PROFILE_SHARE_REVIEW_OUTPUT
if (!output) throw new Error('PROFILE_SHARE_REVIEW_OUTPUT is required')
await mkdir(output, { recursive: true })
const browser = await chromium.launch({ headless: true })
const report = []
const now = Math.floor(Date.now() / 1000)
try {
  for (const width of [1440, 390]) {
    const context = await browser.newContext({
      viewport: { width, height: 1100 },
      locale: 'zh-CN',
      colorScheme: 'dark',
      reducedMotion: 'reduce',
      serviceWorkers: 'block',
    })
    await context.addCookies([
      { name: 'vite-ui-theme', value: 'dark', url: origin },
    ])
    await context.addInitScript(() => {
      localStorage.setItem('i18nextLng', 'zhCN')
      localStorage.setItem('lmm:source-consent:v2', 'no')
    })
    let modelsEnabled = false
    const errors = [],
      requests = []
    await context.route('**/*', async (route) => {
      const request = route.request()
      const url = new URL(request.url())
      // Public SVG images are rendered by Go fixtures generated in this job.
      if (url.pathname.startsWith('/api/share/profile/')) {
        const theme = url.searchParams.get('theme') ?? 'dark'
        const body = await readFile(
          path.join(output, `models-${theme}.svg`),
          'utf8'
        )
        return route.fulfill({ contentType: 'image/svg+xml', body })
      }
      if (url.origin !== origin) return route.abort('blockedbyclient')
      if (url.pathname === '/api/status') {
        return route.fulfill({
          json: {
            success: true,
            data: {
              system_name: 'LMM Best',
              logo: '/logo.png',
              assistant: { enabled: false },
              announcements_enabled: false,
            },
          },
        })
      }
      if (!url.pathname.startsWith('/api/')) return route.continue()
      requests.push(`${request.method()} ${url.pathname}`)
      errors.push(`NETWORK: ${request.method()} ${url.pathname}`)
      return route.abort('blockedbyclient')
    })
    const page = await context.newPage()
    page.setDefaultTimeout(30000)
    page.on('pageerror', (error) => errors.push(String(error)))
    page.on('console', (message) => {
      if (message.type() === 'error') errors.push(message.text())
    })
    try {
      await page.goto(
        `${origin}/profile/share?debug_persona=l1&console_review=1`,
        { waitUntil: 'domcontentloaded', timeout: 90000 }
      )
      await page.getByTestId('persona-debug-trigger').waitFor()
      await page.locator('#badge-layout').waitFor()
      assert.equal(await page.locator('#badge-layout').inputValue(), 'models')
      assert.equal(
        await page.locator('textarea[aria-label="SVG URL"]').count(),
        0
      )
      await page
        .getByRole('button', { name: '开启模型用量分享', exact: true })
        .click()
      const image = page.locator('img[src*="/api/share/profile/"]')
      await image.waitFor()
      await page.waitForFunction(() =>
        [...document.images].some(
          (image) =>
            image.src.includes('/api/share/profile/') &&
            image.complete &&
            image.naturalWidth > 0
        )
      )
      assert.equal(
        await page.locator('#badge-period option[value="all"]').count(),
        0
      )
      const svgURL = page.locator('textarea[aria-label="SVG URL"]')
      assert.equal(
        new URL(await svgURL.inputValue()).searchParams.get('lang'),
        'zh'
      )
      assert.equal(
        new URL(await svgURL.inputValue()).searchParams.get('animation'),
        'none'
      )
      assert.match(
        await page.locator('textarea[aria-label="GitHub README"]').inputValue(),
        /^\[!\[LMM Best model usage\]/
      )
      assert.ok(
        !(
          await page
            .locator('textarea[aria-label="GitHub README"]')
            .inputValue()
        ).includes('###')
      )
      await image.scrollIntoViewIfNeeded()
      await page.screenshot({
        path: path.join(output, `model-share-${width}.png`),
        fullPage: true,
        animations: 'disabled',
      })
      await page.getByRole('button', { name: 'Paper', exact: true }).click()
      await page.waitForFunction(() =>
        [...document.images].some(
          (image) =>
            image.src.includes('theme=paper') &&
            image.complete &&
            image.naturalWidth > 0
        )
      )
      await image.scrollIntoViewIfNeeded()
      await page.screenshot({
        path: path.join(output, `model-share-paper-${width}.png`),
        fullPage: true,
        animations: 'disabled',
      })
      await page.locator('#badge-top').selectOption('3')
      assert.equal(
        new URL(await svgURL.inputValue()).searchParams.get('top'),
        '3'
      )
      await page.locator('#badge-period').selectOption('7d')
      assert.equal(
        new URL(await svgURL.inputValue()).searchParams.get('period'),
        '7d'
      )
      await page
        .locator('summary')
        .filter({ hasText: '查看完整模型统计' })
        .click()
      await page.getByRole('button', { name: '365 天', exact: true }).click()
      assert.equal(
        new URL(await svgURL.inputValue()).searchParams.get('period'),
        '365d'
      )
      await page.locator('#badge-layout').selectOption('profile')
      assert.equal(
        new URL(await svgURL.inputValue()).searchParams.has('top'),
        false
      )
      await page.locator('#badge-layout').selectOption('models')
      await page
        .getByRole('button', { name: '关闭模型用量分享', exact: true })
        .click()
      await page
        .getByRole('button', { name: '开启模型用量分享', exact: true })
        .waitFor()
      assert.equal(await svgURL.count(), 0)
      assert.equal(await image.count(), 0)
      const dimensions = await page.evaluate(() => ({
        viewport: innerWidth,
        scroll: document.documentElement.scrollWidth,
      }))
      assert.ok(dimensions.scroll <= dimensions.viewport + 1)
      assert.deepEqual(errors, [])
      report.push({ width, dimensions, requests, errors })
    } catch (error) {
      await page.screenshot({
        path: path.join(output, `failure-${width}.png`),
        fullPage: true,
      })
      await writeFile(
        path.join(output, `failure-${width}.txt`),
        `${error}\n${await page.locator('body').innerText()}\n${JSON.stringify({ errors, requests })}`
      )
      throw error
    } finally {
      await context.close()
    }
  }
} finally {
  await writeFile(
    path.join(output, 'browser-report.json'),
    JSON.stringify(report, null, 2)
  )
  await browser.close()
}
