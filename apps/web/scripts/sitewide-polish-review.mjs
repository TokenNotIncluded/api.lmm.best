/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ?? 'playwright')
const url = new URL(
  process.env.CONSOLE_REVIEW_ORIGIN ?? 'http://127.0.0.1:4174'
)
assert.equal(url.origin, `http://127.0.0.1:${url.port}`)
assert.equal(url.pathname, '/')
assert.equal(url.username + url.password + url.search + url.hash, '')
const output = process.env.POLISH_REVIEW_OUTPUT
assert.ok(output, 'POLISH_REVIEW_OUTPUT is required')
await mkdir(output, { recursive: true })
const browser = await chromium.launch({
  headless: true,
  executablePath: process.env.PLAYWRIGHT_CHROME_EXECUTABLE,
})
const report = []
const widths = process.env.POLISH_REVIEW_WIDTHS?.split(',').map(Number) ?? [
  320, 390, 430, 834, 1440,
]
assert.ok(
  widths.length > 0 &&
    widths.every((width) => [320, 390, 430, 834, 1440].includes(width))
)

try {
  for (const width of widths) {
    for (const theme of ['light', 'dark']) {
      for (const language of width === 320 ? ['zhCN', 'en'] : ['zhCN']) {
        const name = `bounties-${width}-${theme}-${language}`
        const context = await browser.newContext({
          viewport: { width, height: width < 768 ? 844 : 1000 },
          hasTouch: width < 768,
          reducedMotion: 'reduce',
          locale: language === 'en' ? 'en-US' : 'zh-CN',
          serviceWorkers: 'block',
        })
        await context.addCookies([
          { name: 'vite-ui-theme', value: theme, url: url.origin },
        ])
        await context.addInitScript((lng) => {
          localStorage.setItem('i18nextLng', lng)
          localStorage.setItem('lmm:source-consent:v2', 'no')
        }, language)
        const errors = []
        await context.route('**/*', (route) => {
          const requestUrl = new URL(route.request().url())
          if (requestUrl.origin !== url.origin) {
            return route.abort('blockedbyclient')
          }
          if (requestUrl.pathname === '/api/status') {
            return route.fulfill({
              json: {
                success: true,
                data: {
                  system_name: 'LMM',
                  assistant: { enabled: true },
                  announcements_enabled: false,
                },
              },
            })
          }
          if (requestUrl.pathname.startsWith('/api/')) {
            errors.push(
              `Unmocked request: ${route.request().method()} ${requestUrl.pathname}`
            )
            return route.abort('blockedbyclient')
          }
          return route.continue()
        })
        const page = await context.newPage()
        page.on('pageerror', (error) => errors.push(error.message))
        try {
          await page.goto(
            `${url.origin}/open-source-bounties?debug_persona=l1&console_review=1`,
            { waitUntil: 'domcontentloaded' }
          )
          await page
            .getByTestId('persona-debug-trigger')
            .waitFor({ timeout: 45000 })
          await page.locator('.console-bounties [role="tab"]').first().waitFor()
          await page.evaluate(() => document.fonts.ready)
          await page.waitForTimeout(800)
          const tabs = page
            .locator('.console-bounties [role="tablist"]')
            .first()
          const geometry = await tabs.evaluate((list) => {
            const box = list.getBoundingClientRect()
            return {
              height: box.height,
              rows: new Set(
                [...list.querySelectorAll('[role="tab"]')].map((tab) =>
                  Math.round(tab.getBoundingClientRect().top)
                )
              ).size,
            }
          })
          assert.equal(geometry.rows, 1)
          assert.ok(
            geometry.height >= 44 && geometry.height <= 56,
            JSON.stringify(geometry)
          )
          assert.equal(await tabs.getByRole('tab').count(), 5)
          const funding = page.locator('.bounty-funding-details')
          assert.equal(await funding.getAttribute('open'), null)
          assert.ok(
            (await page
              .locator('.console-bounties [data-slot="card"]')
              .count()) >= 1,
            'Use populated review fixtures, not only an empty page'
          )
          const firstCard = await page
            .locator('.console-bounties [data-slot="card"]')
            .first()
            .boundingBox()
          assert.ok(
            firstCard && firstCard.y < 520,
            `First bounty is buried: ${JSON.stringify(firstCard)}`
          )
          const overflow = await page.evaluate(
            () => document.documentElement.scrollWidth - innerWidth
          )
          assert.ok(overflow <= 1)
          await page.screenshot({ path: `${output}/${name}.png` })

          // Keyboard selection must reveal offscreen labels without dragging the document.
          await tabs.getByRole('tab').first().focus()
          await page.keyboard.press('End')
          await page.keyboard.press('Enter')
          await page.waitForTimeout(250)
          const focused = await tabs.evaluate((list) => {
            const active = document.activeElement
            const item = active?.getBoundingClientRect()
            const box = list.getBoundingClientRect()
            return {
              inside: !!active && list.contains(active),
              visible:
                !!item &&
                item.left >= box.left - 2 &&
                item.right <= box.right + 2,
            }
          })
          assert.ok(focused.inside && focused.visible, JSON.stringify(focused))
          await tabs.getByRole('tab').first().click()
          await funding.locator('summary').click()
          assert.equal(await funding.evaluate((node) => node.open), true)
          await page.screenshot({ path: `${output}/${name}-funding.png` })
          await funding.locator('summary').click()

          const service = page.locator('.console-service-notice')
          await service.locator('summary').click()
          const popup = page.locator('.console-service-notice-content')
          await popup.waitFor({ state: 'visible' })
          const popupIsNotClipped = await popup.evaluate((node) => {
            const box = node.getBoundingClientRect()
            const target = document.elementFromPoint(
              box.left + box.width / 2,
              box.top + 10
            )
            return (
              !!target &&
              node.contains(target) &&
              box.top >= 0 &&
              box.bottom <= innerHeight
            )
          })
          assert.ok(
            popupIsNotClipped,
            'Service and privacy controls must not be clipped by the collapsing footer'
          )
          await page.screenshot({ path: `${output}/${name}-privacy.png` })
          await service.locator('summary').click()

          if (width < 768) {
            const footer = page.locator('.console-shell-footer')
            const footerBox = await footer.boundingBox()
            assert.ok(
              footerBox && footerBox.height <= 76,
              JSON.stringify(footerBox)
            )
            const scroll = page.locator('.console-bounties')
            // Move focus out of footer first. Keyboard-focused controls intentionally stay visible.
            await page.locator('.console-bounties h1').click()
            await scroll.hover({ position: { x: width / 2, y: 420 } })
            await page.mouse.wheel(0, 600)
            await page.waitForTimeout(400)
            await page.mouse.wheel(0, 250)
            await page.waitForTimeout(400)
            assert.equal(
              await footer.locator('xpath=../..').getAttribute('data-hidden'),
              'true'
            )
            await page.keyboard.press('Tab')
            await page.waitForTimeout(250)
            assert.equal(
              await footer.locator('xpath=../..').getAttribute('data-hidden'),
              'false'
            )
          }
          assert.deepEqual(errors, [])
          report.push({ name, ok: true, geometry, firstCard })
        } catch (error) {
          report.push({ name, ok: false, error: String(error), errors })
          await page
            .screenshot({ path: `${output}/${name}-failed.png` })
            .catch(() => {})
        } finally {
          await context.close()
          await writeFile(
            `${output}/report.json`,
            JSON.stringify(report, null, 2)
          )
        }
      }
    }
  }
} finally {
  await browser.close()
}
console.log(JSON.stringify(report, null, 2))
assert.ok(
  report.length > 0 && report.every((entry) => entry.ok),
  'Inspect the sitewide polish report'
)
