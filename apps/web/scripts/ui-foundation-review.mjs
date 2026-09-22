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

async function settle(page) {
  await page.evaluate(async () => {
    await document.fonts.ready
    await new Promise(requestAnimationFrame)
    await new Promise(requestAnimationFrame)
    const animations = document
      .getAnimations()
      .filter((animation) =>
        Number.isFinite(animation.effect?.getComputedTiming().endTime)
      )
    await Promise.all(
      animations.map((animation) => animation.finished.catch(() => undefined))
    )
  })
}

async function capture(page, name, errors, fullPage = true) {
  await settle(page)
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
    fullPage,
    animations: 'disabled',
  })
  report.push({
    name,
    viewport: page.viewportSize(),
    capture: fullPage ? 'document' : 'viewport',
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
      // The shared HTML template includes this optional tracker. Block it in
      // the isolated preview; never load it or count it as application traffic.
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
      await draft.waitFor({ state: 'hidden' })
      assert.equal(await draft.isVisible(), false)
      await page.getByRole('tab', { name: '概览', exact: true }).click()
      await draft.waitFor({ state: 'visible' })
      assert.equal(await draft.inputValue(), '切换后保留')
      const toggle = page.getByRole('switch', { name: '自动刷新' })
      const compactToggle = page.getByRole('switch', { name: '紧凑模式' })
      for (const control of [toggle, compactToggle]) {
        // Exercise actual keyboard state changes in both writing directions.
        const initial = await control.getAttribute('aria-checked')
        for (const direction of ['ltr', 'rtl']) {
          await control.evaluate(
            (el, dir) => el.setAttribute('dir', dir),
            direction
          )
          for (const checked of ['true', 'false']) {
            if ((await control.getAttribute('aria-checked')) !== checked) {
              await control.focus()
              await page.keyboard.press('Space')
            }
            assert.equal(await control.getAttribute('aria-checked'), checked)
            await settle(page)
            const track = await control.boundingBox()
            const thumb = await control
              .locator('[data-slot=switch-thumb]')
              .boundingBox()
            assert.ok(track && thumb)
            assert.ok(
              thumb.x >= track.x - 1 &&
                thumb.x + thumb.width <= track.x + track.width + 1,
              `Switch thumb escaped ${direction} track: ${JSON.stringify({ track, thumb, checked })}`
            )
            const center = thumb.x + thumb.width / 2
            const trackCenter = track.x + track.width / 2
            const onRight = (direction === 'ltr') === (checked === 'true')
            assert.ok(onRight ? center > trackCenter : center < trackCenter)
          }
        }
        await control.evaluate((el) => el.removeAttribute('dir'))
        if ((await control.getAttribute('aria-checked')) !== initial) {
          await control.click()
        }
      }
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
      await capture(page, `otp-complete-${width}`, errors)
      await slots.last().focus()
      await page.keyboard.press('Backspace')
      assert.equal(await slots.last().inputValue(), '')
      assert.equal(await page.getByTestId('otp-submit-count').innerText(), '1')
      assert.equal(
        await form.evaluate((el) => new FormData(el).get('otp')),
        '12345'
      )
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
      await capture(page, `dialog-${width}`, errors, false)
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
      await capture(page, `drawer-${width}`, errors, false)
      await page.keyboard.press('Escape')
      await drawer.waitFor({ state: 'hidden' })
      assert.equal(
        await drawerTrigger.evaluate((el) => el === document.activeElement),
        true
      )
      await page.emulateMedia({ reducedMotion: 'reduce' })
      // Tailwind transition-none disables the property; its inherited duration
      // may remain nonzero without producing any transition.
      const transitionProperty = await toggle.evaluate(
        (el) => getComputedStyle(el).transitionProperty
      )
      assert.equal(transitionProperty, 'none')
    } catch (error) {
      await page.screenshot({
        path: path.join(output, `failed-${width}.png`),
        fullPage: true,
      })
      report.push({ width, error: String(error), stack: error.stack, errors })
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
const failures = report.filter((entry) => entry.error)
if (failures.length) {
  throw new Error(
    `${failures.length} foundation viewport checks failed; see ui-report.json`
  )
}
console.log(
  `UI foundation: ${report.length} views and interaction contracts passed`
)
