/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ?? 'playwright')
const origin = 'http://127.0.0.1:4174'
const output = process.env.CONSOLE_REVIEW_OUTPUT
if (!output) throw new Error('CONSOLE_REVIEW_OUTPUT is required')
await mkdir(output, { recursive: true })
const browser = await chromium.launch({ headless: true })
const report = []

async function capture(page, route, name, errors) {
  await page.evaluate(() => document.fonts.ready)
  await page.screenshot({
    path: path.join(output, name),
    animations: 'disabled',
    fullPage: true,
  })
  const dimensions = await page.evaluate(() => ({
    width: innerWidth,
    scroll: document.documentElement.scrollWidth,
  }))
  const text = await page.locator('body').innerText()
  assert.ok(
    !/Invalid language tag|Internal Server Error|PERSONA_DEBUG_UNMOCKED_REQUEST/.test(
      text
    )
  )
  assert.ok(dimensions.scroll <= dimensions.width + 1)
  assert.deepEqual(errors, [])
  assert.equal(
    await page
      .locator('[data-sonner-toast][data-type="error"]:visible')
      .count(),
    0
  )
  report.push({
    persona: 'l1',
    path: route,
    screenshot: name,
    dimensions,
    errors: [...errors],
  })
}

try {
  for (const width of [1440, 390, 320]) {
    const context = await browser.newContext({
      viewport: { width, height: width > 640 ? 1000 : 844 },
      locale: 'zh-CN',
      serviceWorkers: 'block',
    })
    await context.addInitScript(() => {
      localStorage.setItem('i18nextLng', 'zhCN')
      localStorage.setItem('lmm:source-consent:v2', 'no')
    })
    const errors = []
    await context.route('**/*', async (route) => {
      const url = new URL(route.request().url())
      if (url.origin !== origin) return route.abort('blockedbyclient')
      if (url.pathname === '/api/status') {
        return route.fulfill({
          json: {
            success: true,
            data: {
              system_name: 'LMM Best',
              assistant: { enabled: true },
              announcements_enabled: false,
            },
          },
        })
      }
      if (url.pathname.startsWith('/api/')) {
        errors.push(`Unexpected backend request: ${url.pathname}`)
        return route.abort('blockedbyclient')
      }
      return route.continue()
    })
    const page = await context.newPage()
    page.setDefaultTimeout(10000)
    page.on('pageerror', (error) => errors.push(String(error)))
    try {
      await page.goto(`${origin}/keys?debug_persona=l1&console_review=1`)
      await page.getByTestId('persona-debug-trigger').waitFor()
      const toolbar = page.locator('.console-toolbar')
      const toggle = toolbar.getByRole('button', { name: /^(Filters|筛选器)/ })
      await toggle.click()
      const status = toolbar.getByRole('button', { name: /^(Status|状态)$/ })
      await status.click()
      await page
        .getByRole('option', { name: /^(Enabled|已启用)(?:\s|$)/ })
        .click()
      await page.keyboard.press('Escape')
      await toggle.click()
      const chips = toolbar.getByRole('group', {
        name: /^(Filters active|筛选已启用)$/,
      })
      await chips.waitFor({ state: 'visible' })
      assert.equal(await toggle.getAttribute('aria-expanded'), 'false')
      if (width < 640) {
        const box = await toggle.boundingBox()
        assert.ok(box && box.height >= 44)
      }
      await capture(page, '/keys', `l1-keys-${width}-active-filter.png`, errors)
      if (width === 1440) {
        await page.evaluate(() =>
          document.documentElement.classList.add('dark')
        )
        await capture(
          page,
          '/keys',
          'l1-keys-1440-active-filter-dark.png',
          errors
        )
        await page.evaluate(() =>
          document.documentElement.classList.remove('dark')
        )
      }
      await chips.getByRole('button').click()
      await chips.waitFor({ state: 'detached' })
      assert.equal(
        await toggle.evaluate((el) => el === document.activeElement),
        true
      )
      await toggle.click()
      assert.equal((await status.innerText()).trim(), '状态')
      await toggle.click()
      if (width < 640) {
        await page.evaluate(() => {
          history.pushState({}, '', '/usage-logs/common?console_review=1')
          window.dispatchEvent(new PopStateEvent('popstate'))
        })
        const logs = page.locator('.console-log-toolbar')
        await logs.waitFor()
        await logs.getByRole('button', { name: /^(Filter|筛选)$/ }).click()
        const drawer = page.locator('.console-log-filter-drawer')
        await drawer.waitFor({ state: 'visible' })
        const rect = await drawer.boundingBox()
        assert.ok(rect && rect.x >= -1 && rect.x + rect.width <= width + 1)
        await capture(
          page,
          '/usage-logs/common',
          `l1-logs-${width}-filter-drawer.png`,
          errors
        )
        await page.keyboard.press('Escape')
        await page.evaluate(() => {
          history.pushState({}, '', '/temporary-activations?console_review=1')
          window.dispatchEvent(new PopStateEvent('popstate'))
        })
        await page.locator('#sms-purchase-balance-notice').waitFor()
        await capture(
          page,
          '/temporary-activations',
          `l1-sms-${width}-low-balance.png`,
          errors
        )
      }
    } catch (error) {
      await page.screenshot({
        path: path.join(output, `interaction-${width}-failed.png`),
        fullPage: true,
      })
      report.push({ width, error: String(error), errors })
      throw error
    } finally {
      await context.close()
    }
  }
} finally {
  await writeFile(
    path.join(output, 'interactions.json'),
    JSON.stringify(report, null, 2)
  )
  await browser.close()
}
console.log(`Console interaction review: ${report.length} views passed`)
