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

async function capture(page, name, errors) {
  await page.evaluate(() => document.fonts.ready)
  const dimensions = await page.evaluate(() => ({
    width: innerWidth,
    scroll: document.documentElement.scrollWidth,
  }))
  assert.ok(
    dimensions.scroll <= dimensions.width + 1,
    JSON.stringify(dimensions)
  )
  assert.deepEqual(errors, [])
  await page.screenshot({
    path: path.join(output, `${name}.png`),
    fullPage: true,
    animations: 'disabled',
  })
  report.push({
    name,
    viewport: page.viewportSize(),
    dimensions,
    errors: [...errors],
  })
}

try {
  for (const width of [1440, 834, 390, 320]) {
    const context = await browser.newContext({
      viewport: { width, height: 1000 },
      locale: 'zh-CN',
      serviceWorkers: 'block',
    })
    const errors = []
    await context.addInitScript(() =>
      localStorage.setItem('i18nextLng', 'zhCN')
    )
    await context.route('**/*', (route) => {
      const url = new URL(route.request().url())
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
      await page.goto(`${origin}/?ui_review=1`, {
        waitUntil: 'domcontentloaded',
      })
      await page.getByTestId('ui-foundation-preview').waitFor()
      await capture(page, `components-${width}-light`, errors)
      await page.getByRole('button', { name: '切换主题' }).click()
      await capture(page, `components-${width}-dark`, errors)
      await page.getByRole('button', { name: '切换主题' }).click()
      const draft = page.getByRole('textbox', { name: '保留的草稿' })
      await draft.fill('切换后保留')
      await page.getByRole('tab', { name: '历史记录', exact: true }).click()
      assert.equal(await draft.isVisible(), false)
      await page.getByRole('tab', { name: '概览', exact: true }).click()
      assert.equal(await draft.inputValue(), '切换后保留')
      const toggle = page.getByRole('switch', { name: '自动刷新' })
      assert.equal(await toggle.getAttribute('aria-checked'), 'true')
      await toggle.click()
      assert.equal(await toggle.getAttribute('aria-checked'), 'false')
      await toggle.click()
      await page.waitForTimeout(250)
      const track = await toggle.boundingBox()
      const thumb = await toggle
        .locator('[data-slot=switch-thumb]')
        .boundingBox()
      assert.ok(
        track &&
          thumb &&
          thumb.x >= track.x &&
          thumb.x + thumb.width <= track.x + track.width + 1
      )
      const form = page.getByTestId('otp-preview-form')
      const slots = form.locator('[data-slot=input-otp-slot]')
      assert.equal(await slots.count(), 6)
      await form.getByRole('button', { name: '验证', exact: true }).click()
      await page.waitForFunction(
        () =>
          document.activeElement?.getAttribute('data-slot') === 'input-otp-slot'
      )
      assert.equal(await slots.first().getAttribute('aria-invalid'), 'true')
      await slots.first().evaluate((input) => {
        const clipboard = new DataTransfer()
        clipboard.setData('text/plain', '12 34 56')
        input.dispatchEvent(
          new ClipboardEvent('paste', {
            clipboardData: clipboard,
            bubbles: true,
            cancelable: true,
          })
        )
      })
      await page.waitForFunction(
        () =>
          [...document.querySelectorAll('[data-slot=input-otp-slot]')]
            .map((el) => el.value)
            .join('') === '123456'
      )
      assert.equal(await page.getByTestId('otp-submit-count').innerText(), '0')
      assert.equal(
        await form.evaluate((el) => new FormData(el).get('otp')),
        '123456'
      )
      await form.getByRole('button', { name: '验证', exact: true }).click()
      await page
        .getByTestId('otp-submit-count')
        .filter({ hasText: '1' })
        .waitFor()
      const dialogTrigger = page.getByRole('button', {
        name: '打开弹窗',
        exact: true,
      })
      await dialogTrigger.click()
      const dialog = page.getByRole('dialog', { name: '连接设置' })
      await dialog.getByRole('combobox', { name: '连接模式' }).click()
      await page
        .getByRole('option', { name: '低延迟连接', exact: true })
        .click()
      assert.equal(await dialog.isVisible(), true)
      await capture(page, `dialog-${width}`, errors)
      await dialog.getByRole('button', { name: '完成', exact: true }).click()
      await dialog.waitFor({ state: 'hidden' })
      assert.equal(
        await dialogTrigger.evaluate((el) => el === document.activeElement),
        true
      )
      const drawerTrigger = page.getByRole('button', {
        name: '打开抽屉',
        exact: true,
      })
      await drawerTrigger.click()
      const drawer = page.getByRole('dialog', { name: '筛选记录' })
      await drawer.waitFor()
      await drawer
        .getByRole('textbox', { name: '搜索', exact: true })
        .fill('示例记录')
      await drawer
        .getByRole('textbox', { name: '抽屉备注' })
        .fill('手势不能吃掉输入')
      const rect = await drawer.boundingBox()
      assert.ok(
        rect &&
          rect.width > 200 &&
          rect.x >= -1 &&
          rect.x + rect.width <= width + 1
      )
      await capture(page, `drawer-${width}`, errors)
      await page.keyboard.press('Escape')
      await drawer.waitFor({ state: 'hidden' })
      assert.equal(
        await drawerTrigger.evaluate((el) => el === document.activeElement),
        true
      )
      await page.emulateMedia({ reducedMotion: 'reduce' })
      const duration = await toggle.evaluate(
        (el) => getComputedStyle(el).transitionDuration
      )
      assert.equal(duration, '0s')
    } catch (error) {
      await page.screenshot({
        path: path.join(output, `failed-${width}.png`),
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
    path.join(output, 'ui-report.json'),
    JSON.stringify(report, null, 2)
  )
  await browser.close()
}
console.log(
  `UI foundation: ${report.length} views and interaction contracts passed`
)
