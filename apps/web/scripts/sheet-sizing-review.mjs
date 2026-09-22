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
const output = path.resolve(process.env.CONSOLE_REVIEW_OUTPUT ?? 'ui-review')
await mkdir(output, { recursive: true })
const browser = await chromium.launch({ headless: true })
const report = []
const viewports = [
  { width: 320, height: 740 },
  { width: 390, height: 844 },
  { width: 640, height: 360 },
  { width: 834, height: 1112 },
  { width: 1023, height: 768 },
  { width: 1024, height: 768 },
  { width: 1440, height: 900 },
]

async function settle(page) {
  await page.evaluate(async () => {
    await document.fonts.ready
    await new Promise(requestAnimationFrame)
    await new Promise(requestAnimationFrame)
    await Promise.all(
      document
        .getAnimations()
        .filter((animation) =>
          Number.isFinite(animation.effect?.getComputedTiming().endTime)
        )
        .map((animation) => animation.finished.catch(() => undefined))
    )
  })
}

async function assertFooterUsable(sheet, viewport) {
  const button = sheet.getByRole('button', { name: '保存', exact: true })
  const box = await button.boundingBox()
  assert.ok(box && box.width > 0 && box.height > 0)
  assert.ok(box.y >= 0 && box.y + box.height <= viewport.height + 1)
  assert.ok(box.x >= 0 && box.x + box.width <= viewport.width + 1)
  assert.equal(
    await button.evaluate((element) => {
      const rect = element.getBoundingClientRect()
      return element.contains(
        document.elementFromPoint(
          rect.left + rect.width / 2,
          rect.top + rect.height / 2
        )
      )
    }),
    true,
    'The save button must be visible and not covered by scrolling content'
  )
}

try {
  for (const viewport of viewports) {
    const context = await browser.newContext({
      viewport,
      locale: 'zh-CN',
      serviceWorkers: 'block',
    })
    const errors = []
    await context.route('**/*', (route) => {
      const url = new URL(route.request().url())
      if (
        url.origin === 'https://cdn.agentlane.com' &&
        url.pathname === '/v1/snippet.js'
      ) {
        return route.abort('blockedbyclient')
      }
      if (url.origin !== origin || url.pathname.startsWith('/api/')) {
        errors.push(`Unexpected request: ${url.origin}${url.pathname}`)
        return route.abort('blockedbyclient')
      }
      return route.continue()
    })
    const page = await context.newPage()
    page.setDefaultTimeout(12000)
    page.on('pageerror', (error) => errors.push(String(error)))
    try {
      await page.goto(`${origin}/?ui_review=1&sheet_review=1`, {
        waitUntil: 'domcontentloaded',
      })
      await page.getByTestId('sheet-sizing-preview').waitFor()
      for (const theme of ['light', 'dark']) {
        const dark = await page.evaluate(() =>
          document.documentElement.classList.contains('dark')
        )
        if (dark !== (theme === 'dark')) {
          await page.getByTestId('sheet-theme-toggle').click()
        }
        for (const side of ['right', 'left']) {
          for (const wide of [false, true]) {
            const id = `${side}-${wide ? 'wide' : 'default'}`
            const trigger = page.getByTestId(`open-${id}`)
            await trigger.click()
            const sheet = page.getByTestId(id)
            await sheet.waitFor()
            await settle(page)
            const dimensions = await sheet.evaluate((element) => {
              const rect = element.getBoundingClientRect()
              const form = element.querySelector('form')
              const fields = element.querySelector('[data-testid=sheet-fields]')
              return {
                x: rect.x,
                width: rect.width,
                rootFontSize: parseFloat(
                  getComputedStyle(document.documentElement).fontSize
                ),
                contentWidth: fields.getBoundingClientRect().width,
                overflow: [element, form, fields].map((node) => ({
                  client: node.clientWidth,
                  scroll: node.scrollWidth,
                })),
              }
            })
            const expected = wide
              ? Math.min(viewport.width, 64 * dimensions.rootFontSize)
              : Math.min(
                  viewport.width * 0.75,
                  viewport.width >= 640
                    ? 24 * dimensions.rootFontSize
                    : Infinity
                )
            assert.ok(
              Math.abs(dimensions.width - expected) <= 1,
              `${id}: expected ${expected}px, got ${JSON.stringify(dimensions)}`
            )
            assert.ok(dimensions.x >= -1)
            assert.ok(dimensions.x + dimensions.width <= viewport.width + 1)
            for (const size of dimensions.overflow) {
              assert.ok(size.scroll <= size.client + 1, JSON.stringify(size))
            }
            assert.ok(
              dimensions.contentWidth >= (wide ? 240 : 180),
              `Fields collapsed: ${JSON.stringify(dimensions)}`
            )
            await assertFooterUsable(sheet, viewport)
            const firstInput = sheet.locator('input').first()
            await firstInput.fill('https://changed.example.test/v1')
            const note = sheet.getByTestId('sheet-note')
            await note.fill('滚动到底部后仍可编辑和保存')
            await assertFooterUsable(sheet, viewport)
            assert.equal(
              await firstInput.inputValue(),
              'https://changed.example.test/v1'
            )
            await sheet.getByRole('button', { name: '保存', exact: true }).click()
            assert.equal(await page.getByTestId('sheet-saved').innerText(), id)
            // Record the form at the top, with the fixed footer still visible.
            await sheet.locator('form').evaluate((form) => {
              form.scrollTop = 0
            })
            await settle(page)
            const name = `sheet-${id}-${viewport.width}-${theme}`
            await page.screenshot({
              path: path.join(output, `${name}.png`),
              animations: 'disabled',
            })
            assert.deepEqual(errors, [])
            report.push({ name, viewport, dimensions, errors: [...errors] })
            await page.keyboard.press('Escape')
            await sheet.waitFor({ state: 'hidden' })
            assert.equal(
              await trigger.evaluate(
                (element) => element === document.activeElement
              ),
              true
            )
          }
        }
      }
    } catch (error) {
      await page.screenshot({
        path: path.join(output, `sheet-failed-${viewport.width}.png`),
      })
      report.push({ viewport, error: String(error), stack: error.stack, errors })
    } finally {
      await context.close()
    }
  }
} finally {
  await writeFile(
    path.join(output, 'sheet-sizing-report.json'),
    JSON.stringify(report, null, 2)
  )
  await browser.close()
}
const failures = report.filter((entry) => entry.error)
if (failures.length) {
  throw new Error(
    `${failures.length} sheet sizing checks failed; see sheet-sizing-report.json`
  )
}
console.log(`Sheet sizing: ${report.length} viewport/theme/side checks passed`)
