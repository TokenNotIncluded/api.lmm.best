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

async function captureInternalSurface(page, prefix, width) {
  const content = page.locator('main.console-page > .console-section-content')
  const captures = []
  let top = 0
  await page.evaluate(() => window.scrollTo(0, 0))
  // This console scrolls inside main. Document fullPage cannot capture it;
  // overlapping real viewports cover every part without changing the layout.
  for (let index = 0; index < 24; index += 1) {
    await content.evaluate(async (element, scrollTop) => {
      element.scrollTop = scrollTop
      await new Promise((resolve) => requestAnimationFrame(resolve))
    }, top)
    const dimensions = await content.evaluate((element) => ({
      scrollTop: element.scrollTop,
      clientHeight: element.clientHeight,
      scrollHeight: element.scrollHeight,
    }))
    assert.ok(dimensions.clientHeight > 80)
    assert.ok(Math.abs(dimensions.scrollTop - top) <= 1)
    const file =
      index === 0
        ? `${prefix}-${width}.png`
        : `${prefix}-${width}-section-${String(index + 1).padStart(2, '0')}.png`
    await page.screenshot({
      path: path.join(output, file),
      fullPage: false,
      animations: 'disabled',
    })
    captures.push({ file, ...dimensions })
    if (top + dimensions.clientHeight >= dimensions.scrollHeight - 1) {
      return captures
    }
    top = Math.min(
      top + dimensions.clientHeight - 80,
      dimensions.scrollHeight - dimensions.clientHeight
    )
  }
  throw new Error('The complete configuration surface exceeded 24 viewports')
}

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
    const errors = [],
      requests = []
    await context.route('**/*', async (route) => {
      const request = route.request()
      const url = new URL(request.url())
      // Public SVG images are rendered by Go fixtures generated in this job.
      if (url.pathname.startsWith('/api/share/profile/')) {
        const theme = url.searchParams.get('theme') ?? 'dark'
        const body = await readFile(
          path.join(
            output,
            `${url.searchParams.get('layout') === 'aggregate' ? 'aggregate' : 'models'}-${theme}.svg`
          ),
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
      if (
        message.type() === 'error' &&
        !message.text().includes('ERR_BLOCKED_BY_CLIENT')
      ) {
        errors.push(message.text())
      }
    })
    try {
      await page.goto(
        `${origin}/profile/share?debug_persona=l1&console_review=1`,
        { waitUntil: 'domcontentloaded', timeout: 90000 }
      )
      await page.getByTestId('persona-debug-trigger').waitFor()
      await page.locator('#badge-layout').waitFor()
      assert.equal(
        await page.locator('#badge-layout').inputValue(),
        'aggregate'
      )
      assert.equal(
        await page.locator('textarea[aria-label="SVG URL"]').count(),
        0
      )
      await page.getByTestId('badge-sharing').getByRole('button').click()
      const aggregateImage = page.locator('img[src*="layout=aggregate"]')
      await aggregateImage.waitFor()
      await page.waitForFunction(() =>
        [...document.images].some(
          (image) =>
            image.src.includes('layout=aggregate') &&
            image.complete &&
            image.naturalWidth > 0
        )
      )
      assert.equal(await page.locator('[data-source-status="live"]').count(), 2)
      assert.equal(
        await page.locator('[data-source-status="snapshot"]').count(),
        1
      )
      assert.match(
        await page.getByTestId('lmm-self-profile').innerText(),
        /899,140,697/
      )
      assert.match(
        await page.getByTestId('lmm-self-profile').innerText(),
        /13,815/
      )
      assert.equal(
        await page.locator('[data-source-status="unsupported"]').count(),
        1
      )
      assert.equal(
        await page
          .locator(
            '[data-testid="lmm-self-profile"] [data-source-status="live"]'
          )
          .count(),
        1
      )
      assert.match(
        await page.locator('[data-source-status="snapshot"]').innerText(),
        /117,000,000,000/
      )
      assert.match(
        await page
          .locator(
            '[data-testid="linked-profile-row"] [data-source-status="live"]'
          )
          .innerText(),
        /2026-09-04.*2026-10-03/
      )
      const aggregateCode = await page
        .locator('textarea[aria-label="GitHub README"]')
        .inputValue()
      assert.match(aggregateCode, /^\[!\[AI usage across linked profiles\]/)
      assert.equal(
        new URL(
          await page.locator('textarea[aria-label="SVG URL"]').inputValue()
        ).searchParams.get('animation'),
        'none'
      )
      await page.getByTestId('add-linked-account').click()
      const extraAccount = page.getByTestId('linked-profile-row').last()
      await extraAccount
        .locator('select[id$="-provider"]')
        .selectOption('custom')
      await extraAccount
        .locator('input[type="url"]')
        .fill('https://127.0.0.1/private')
      await page.getByTestId('save-linked-accounts').click()
      await extraAccount.locator('[role="alert"]').waitFor()
      await extraAccount
        .locator('input[type="url"]')
        .fill('https://github.com/profile-fixture')
      await page.getByTestId('save-linked-accounts').click()
      await extraAccount.locator('[data-source-status="unsupported"]').waitFor()
      assert.ok(
        !(
          await extraAccount
            .locator('[data-source-status="unsupported"]')
            .innerText()
        ).includes('0 Tokens')
      )
      await extraAccount
        .getByRole('button', { name: '移除账号 4', exact: true })
        .click()
      await page.getByTestId('save-linked-accounts').click()
      await page.getByText('关联账号已保存。', { exact: true }).waitFor()
      await page.waitForFunction(
        () =>
          document.querySelectorAll('[data-testid="linked-profile-row"]')
            .length === 3 &&
          document.querySelectorAll('[data-source-status="unsupported"]')
            .length === 1 &&
          document.querySelectorAll('[data-source-status="live"]').length ===
            2 &&
          document.querySelectorAll('[data-source-status="snapshot"]')
            .length === 1
      )
      await page.waitForFunction(() =>
        [...document.images].some(
          (image) =>
            image.src.includes('layout=aggregate') &&
            image.complete &&
            image.naturalWidth > 0
        )
      )
      await page
        .locator('main.console-page > .console-section-content')
        .evaluate((element) => {
          element.scrollTop = 0
        })
      const copyAction = await page
        .locator('button[aria-label="复制 README 代码"]')
        .first()
        .evaluate((button) => {
          const bounds = button.getBoundingClientRect()
          const label = [...button.childNodes].find(
            (node) =>
              node.nodeType === Node.TEXT_NODE && node.textContent.trim()
          )
          if (!label) {
            throw new Error('The primary copy action has no visible label')
          }
          const range = document.createRange()
          range.selectNodeContents(label)
          const text = range.getBoundingClientRect()
          return {
            width: bounds.width,
            height: bounds.height,
            clientWidth: button.clientWidth,
            scrollWidth: button.scrollWidth,
            left: bounds.left,
            right: bounds.right,
            labelLeft: text.left,
            labelRight: text.right,
          }
        })
      assert.ok(copyAction.height >= 44)
      assert.ok(copyAction.scrollWidth <= copyAction.clientWidth + 1)
      assert.ok(copyAction.labelLeft >= copyAction.left - 1)
      assert.ok(copyAction.labelRight <= copyAction.right + 1)
      assert.ok(copyAction.left >= 0 && copyAction.right <= width)
      const headingFonts = await page.evaluate(async () => {
        await document.fonts.ready
        const body = document.querySelector(
          '[data-testid="lmm-self-profile"] p'
        )
        if (!body) {
          throw new Error('The native account body is missing')
        }
        const headings = [
          ...document.querySelectorAll(
            '[data-testid="lmm-self-profile"] h3, [data-testid="linked-profile-row"] h3'
          ),
        ]
        return {
          body: getComputedStyle(body).fontFamily,
          headings: headings.map((heading) => ({
            label: heading.textContent.trim(),
            fontFamily: getComputedStyle(heading).fontFamily,
          })),
        }
      })
      await writeFile(
        path.join(output, `heading-fonts-${width}.json`),
        JSON.stringify(headingFonts, null, 2)
      )
      assert.match(headingFonts.body, /Public Sans/)
      assert.equal(headingFonts.headings.length, 4)
      for (const heading of headingFonts.headings) {
        assert.equal(
          heading.fontFamily,
          headingFonts.body,
          `${heading.label} must use the console body font`
        )
      }
      const captures = await captureInternalSurface(
        page,
        'aggregate-share',
        width
      )
      await page.locator('#badge-layout').selectOption('models')
      await page
        .getByTestId('badge-sharing')
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
      await page.locator('summary').filter({ hasText: '自定义外观' }).click()
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
      await page
        .locator('main.console-page > .console-section-content')
        .evaluate((element) => {
          element.scrollTop = 0
        })
      await page.evaluate(() => window.scrollTo(0, 0))
      await page.screenshot({
        path: path.join(output, `model-share-${width}.png`),
        fullPage: false,
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
      await page
        .locator('main.console-page > .console-section-content')
        .evaluate((element) => {
          element.scrollTop = 0
        })
      await page.evaluate(() => window.scrollTo(0, 0))
      await page.screenshot({
        path: path.join(output, `model-share-paper-${width}.png`),
        fullPage: false,
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
        .getByTestId('badge-sharing')
        .getByRole('button', { name: '关闭模型用量分享', exact: true })
        .click()
      await page
        .getByTestId('badge-sharing')
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
      report.push({
        width,
        dimensions,
        copyAction,
        headingFonts,
        captures,
        requests,
        errors,
      })
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
