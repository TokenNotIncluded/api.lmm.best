/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
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
const reply = Array.from(
  { length: 28 },
  (_, index) =>
    `### 第 ${index + 1} 步\n\n这是离线测试回复。上下滑动阅读内容，打开和收起键盘后，输入框仍应可见，阅读位置不应丢失。`
).join('\n\n')

async function geometry(page) {
  return page.evaluate(() => {
    const pane = document.querySelector('.l0-dialogue')
    const outer = document.querySelector('.console-section-content')
    const input = document.querySelector('.l0-input-row')
    const box = input.getBoundingClientRect()
    const paneBox = pane?.getBoundingClientRect()
    return {
      width: innerWidth,
      height: innerHeight,
      documentWidth: document.documentElement.scrollWidth,
      outerHeight: outer.clientHeight,
      outerExtent: outer.scrollHeight,
      outerTop: outer.scrollTop,
      outerOverflow: getComputedStyle(outer).overflowY,
      input: {
        x: box.x,
        y: box.y,
        right: box.right,
        bottom: box.bottom,
        height: box.height,
      },
      paneBottom: paneBox?.bottom,
      top: pane?.scrollTop,
      gap: pane ? pane.scrollHeight - pane.clientHeight - pane.scrollTop : null,
      inputFont: getComputedStyle(document.querySelector('#l0-question'))
        .fontSize,
    }
  })
}

function checkMobile(box) {
  assert.equal(
    box.outerOverflow,
    'hidden',
    'the outer page must not also scroll during chat'
  )
  assert.ok(box.outerExtent <= box.outerHeight + 1, JSON.stringify(box))
  assert.equal(box.outerTop, 0)
  assert.ok(box.input.x >= 0 && box.input.right <= box.width + 1)
  assert.ok(box.input.y >= 0 && box.input.bottom <= box.height + 1)
  assert.ok(
    box.paneBottom <= box.input.y + 1,
    'the composer must not cover messages'
  )
  assert.equal(box.inputFont, '16px')
}

try {
  for (const theme of ['dark', 'light']) {
    for (const width of [320, 390, 430, 767, 1440]) {
      const mobile = width < 768
      const height = mobile ? 844 : 1000
      const context = await browser.newContext({
        viewport: { width, height },
        isMobile: mobile,
        hasTouch: mobile,
        locale: 'zh-CN',
        colorScheme: theme,
        reducedMotion: 'reduce',
        serviceWorkers: 'block',
      })
      const errors = []
      let requests = 0
      await context.addInitScript((theme) => {
        window.__l0ReviewFetch = window.fetch.bind(window)
        localStorage.setItem('i18nextLng', 'zhCN')
        localStorage.setItem('lmm:source-consent:v2', 'no')
        localStorage.setItem('vite-ui-theme', theme)
      }, theme)
      await context.route('**/*', async (route) => {
        const url = new URL(route.request().url())
        if (url.origin !== origin) return route.abort('blockedbyclient')
        if (url.pathname === '/api/assistant/chat') {
          requests++
          return route.fulfill({
            json: { choices: [{ message: { content: reply } }] },
          })
        }
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
      page.setDefaultTimeout(15000)
      page.on('pageerror', (error) => errors.push(String(error)))
      const entry = { width, theme, samples: [], errors }
      report.push(entry)
      try {
        await page.goto(
          `${origin}/getting-started?debug_persona=l0&console_review=1`
        )
        const input = page.locator('#l0-question')
        await input.waitFor()
        // Hide only the development persona control, not production content.
        await page.addStyleTag({
          content: '[data-testid="persona-debug-trigger"] { display: none; }',
        })
        // The dev persona deliberately blocks fetch writes. Permit exactly this
        // synthetic chat request through the browser route above, not production.
        await page.evaluate(() => {
          const personaFetch = window.fetch.bind(window)
          window.fetch = (input, init) => {
            const request = new Request(input, init)
            const url = new URL(request.url, location.origin)
            return url.origin === location.origin &&
              url.pathname === '/api/assistant/chat' &&
              request.method === 'POST'
              ? window.__l0ReviewFetch(request)
              : personaFetch(request)
          }
        })
        await page.evaluate(
          (theme) =>
            document.documentElement.classList.toggle('dark', theme === 'dark'),
          theme
        )
        await input.fill('请说明如何连接客户端。')
        await input.press('Enter')
        await page.locator('.l0-composer[data-phase="done"]').waitFor()
        await page.waitForTimeout(1200)
        await page.screenshot({
          path: path.join(output, `${theme}-${width}-chat.png`),
        })
        const initial = await geometry(page)
        entry.samples.push(initial)
        assert.ok(initial.documentWidth <= width + 1)
        if (mobile) {
          checkMobile(initial)
          assert.ok(initial.gap < 2, JSON.stringify(initial))
          for (const nextHeight of [420, height, 360, height]) {
            await input.focus()
            await page.setViewportSize({ width, height: nextHeight })
            await page.waitForTimeout(150)
            if (nextHeight === height) {
              await input.evaluate((node) => node.blur())
            }
            const box = await geometry(page)
            entry.samples.push(box)
            checkMobile(box)
            assert.ok(
              box.gap < 2,
              'keyboard changes must keep following the last reply'
            )
            await page.screenshot({
              path: path.join(output, `${theme}-${width}-${nextHeight}.png`),
            })
          }
          await page.locator('.l0-dialogue').evaluate((node) => {
            node.scrollTop = 120
          })
          await page.waitForTimeout(100)
          for (const nextHeight of [420, height]) {
            await page.setViewportSize({ width, height: nextHeight })
            await page.waitForTimeout(150)
            const box = await geometry(page)
            checkMobile(box)
            assert.ok(
              Math.abs(box.top - 120) <= 1,
              'do not pull the reader away from older text'
            )
          }
          await input.fill('保留这段未发送的草稿')
          await page.locator('#l0-tab-explore').click()
          await page.locator('#l0-panel-explore').waitFor({ state: 'visible' })
          await page.locator('#l0-tab-chat').click()
          await input.waitFor({ state: 'visible' })
          assert.equal(await input.inputValue(), '保留这段未发送的草稿')
          assert.equal(requests, 1)
          await page.locator('.l0-jump-latest').click()
          await page.waitForTimeout(100)
          assert.ok((await geometry(page)).gap < 2)
          await input.press('Enter')
          await page.locator('.l0-composer[data-phase="done"]').waitFor()
          await page.waitForTimeout(1200)
          assert.equal(requests, 2)
          assert.equal(await page.getByTestId('l0-history-turn').count(), 1)
          checkMobile(await geometry(page))
        }
        assert.deepEqual(errors, [])
        entry.ok = true
      } catch (error) {
        entry.failure = String(error)
        await page
          .screenshot({
            path: path.join(output, `${theme}-${width}-failure.png`),
          })
          .catch(() => {})
      } finally {
        await context.close()
        console.log(JSON.stringify(entry))
      }
    }
  }
} finally {
  await writeFile(
    path.join(output, 'mobile-chat-report.json'),
    JSON.stringify(report, null, 2)
  )
  await browser.close()
}
assert.ok(
  report.every((entry) => entry.ok),
  'mobile chat browser regressions; inspect mobile-chat-report.json'
)
