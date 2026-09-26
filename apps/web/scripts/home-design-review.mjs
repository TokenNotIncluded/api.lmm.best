/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
// Render the real production bundle with fictional, read-only API fixtures.
import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ?? 'playwright')
const origin = process.env.HOME_REVIEW_ORIGIN ?? 'http://127.0.0.1:4175'
const output = process.env.HOME_REVIEW_OUTPUT ?? 'artifacts/home-review'
assert.ok(
  ['127.0.0.1', 'localhost', '[::1]'].includes(new URL(origin).hostname)
)
await mkdir(output, { recursive: true })
const browser = await chromium.launch({ headless: true })
const results = []
try {
  for (const [name, width, height, theme, motion, language] of [
    ['desktop', 1440, 1000, 'light', 'no-preference', 'zhCN'],
    ['mobile', 390, 844, 'light', 'no-preference', 'zhCN'],
    ['compact', 320, 740, 'light', 'no-preference', 'zhCN'],
    ['tablet', 768, 1024, 'light', 'no-preference', 'zhCN'],
    ['english', 390, 844, 'light', 'no-preference', 'en'],
    ['dark', 1440, 1000, 'dark', 'no-preference', 'zhCN'],
    ['reduced', 390, 844, 'light', 'reduce', 'zhCN'],
  ]) {
    const context = await browser.newContext({
      viewport: { width, height },
      locale: 'zh-CN',
      reducedMotion: motion,
      serviceWorkers: 'block',
    })
    await context.addCookies([
      { name: 'vite-ui-theme', value: theme, url: origin },
    ])
    await context.addInitScript((lang) => {
      localStorage.setItem('i18nextLng', lang)
      localStorage.setItem('lmm:source-consent:v2', 'no')
    }, language)
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
      return route.fulfill({ json: { success: true, data } })
    })
    const page = await context.newPage()
    const errors = []
    page.on('pageerror', (error) => errors.push(error.message))
    try {
      await page.goto(origin, { waitUntil: 'networkidle' })
      await page.locator('#lmm-home-title').waitFor()
      await page.waitForFunction(
        () => document.querySelector('.lmm-home')?.dataset.motion !== 'loading'
      )
      await page.waitForTimeout(1200)
      await page.screenshot({ path: `${output}/${name}.png` })
      const metrics = await page.evaluate(() => {
        const box = (selector) => {
          const rect = document.querySelector(selector)?.getBoundingClientRect()
          return (
            rect && {
              x: rect.x,
              y: rect.y,
              width: rect.width,
              height: rect.height,
            }
          )
        }
        return {
          scrollWidth: document.documentElement.scrollWidth,
          motion: document.querySelector('.lmm-home')?.dataset.motion,
          title: box('#lmm-home-title'),
          visual: box('[data-home-visual]'),
          controls: box('.lmm-core-steps'),
          activePanel: box('[data-cinema-panel][data-active]'),
          stageBorder: getComputedStyle(
            document.querySelector('[data-cinema-inner]')
          ).borderTopWidth,
          inputBorder: getComputedStyle(
            document.querySelector('[data-token-input]')
          ).borderTopWidth,
          buttons: Array.from(
            document.querySelectorAll('[data-cinema-jump]')
          ).map((button) => ({
            text: button.textContent,
            width: button.getBoundingClientRect().width,
            height: button.getBoundingClientRect().height,
            unobstructed: (() => {
              const rect = button.getBoundingClientRect()
              const target = document.elementFromPoint(
                rect.x + rect.width / 2,
                rect.y + rect.height / 2
              )
              return target === button || button.contains(target)
            })(),
          })),
        }
      })
      assert.equal(metrics.scrollWidth, width, `${name}: horizontal overflow`)
      assert.deepEqual(errors, [], `${name}: browser exceptions`)
      assert.equal(metrics.stageBorder, '0px', `${name}: framed stage returned`)
      assert.equal(
        metrics.inputBorder,
        '0px',
        `${name}: boxed token input returned`
      )
      if (motion === 'no-preference') {
        for (const button of metrics.buttons) {
          assert.ok(
            button.width >= 44 && button.height >= 44,
            `${name}: undersized navigation target`
          )
          assert.ok(
            button.unobstructed,
            `${name}: navigation covered by a widget`
          )
        }
        assert.ok(
          metrics.activePanel.y + metrics.activePanel.height <
            metrics.controls.y,
          `${name}: navigation overlaps content`
        )
        const toggle = page.locator('[data-motion-toggle]')
        await toggle.click()
        assert.equal(await toggle.getAttribute('aria-pressed'), 'true')
        // Pause before selecting a particle: continuously moving tokens never
        // satisfy Playwright's stable-element auto-wait.
        const token = page.locator('[data-token-option]').first()
        const selected = await token.getAttribute('data-token-option')
        await token.click()
        assert.equal(
          await page.locator('[data-selected-token]').textContent(),
          selected
        )
        assert.ok(await page.locator('[data-predicted-token]').textContent())
        await toggle.click()
        assert.equal(await toggle.getAttribute('aria-pressed'), 'false')
        await page.locator('[data-cinema-jump="1"]').click()
        await page.waitForFunction(() =>
          document
            .querySelector('[data-cinema-panel="1"]')
            ?.hasAttribute('data-active')
        )
        await page.screenshot({ path: `${output}/${name}-api.png` })
        await page.locator('[data-cinema-jump="0"]').click()
      }
      if (['desktop', 'mobile', 'dark'].includes(name)) {
        await page
          .locator('.lmm-assistant-section')
          .screenshot({ path: `${output}/${name}-assistant.png` })
      }
      assert.deepEqual(errors, [], `${name}: interaction exceptions`)
      results.push({ name, width, height, theme, language, ...metrics, errors })
    } catch (error) {
      results.push({ name, failure: String(error), errors })
      await page.screenshot({ path: `${output}/${name}-failure.png` })
    } finally {
      await context.close()
    }
  }
  await writeFile(`${output}/results.json`, JSON.stringify(results, null, 2))
  const failures = results.filter((result) => result.failure)
  console.log(JSON.stringify({ results }))
  assert.deepEqual(failures, [], 'Homepage browser review failed')
} finally {
  await browser.close()
}
