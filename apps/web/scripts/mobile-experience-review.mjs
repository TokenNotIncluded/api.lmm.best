/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ?? 'playwright')
const origin = new URL(
  process.env.MOBILE_PUBLIC_ORIGIN ?? 'http://127.0.0.1:4175'
).origin
const consoleOrigin = process.env.MOBILE_CONSOLE_ORIGIN
for (const value of [origin, consoleOrigin].filter(Boolean)) {
  const url = new URL(value)
  assert.equal(
    url.hostname,
    '127.0.0.1',
    'Review only loopback fixtures, never a live account'
  )
  assert.equal(url.protocol, 'http:')
}
const output = process.env.MOBILE_REVIEW_OUTPUT
assert.ok(output, 'MOBILE_REVIEW_OUTPUT is required')
await mkdir(output, { recursive: true })
const browser = await chromium.launch({
  headless: true,
  executablePath: process.env.PLAYWRIGHT_CHROME_EXECUTABLE,
})
const report = []
const selected = (name) =>
  !process.env.MOBILE_REVIEW_CASES ||
  process.env.MOBILE_REVIEW_CASES.split(',').includes(name)
const documentFixture =
  '# 使用说明\n请逐项阅读以下说明。\n\n## 账号\n使用自己的账号。不要分享密码。\n\n## 密钥\n仅在受信任的软件中填写密钥。\n\n## 计费\n查看实际用量和当前价格。\n\n## 联系支持\n提供请求编号，不要公开密钥。'
const status = {
  system_name: 'LMM',
  register_enabled: true,
  password_register_enabled: true,
  password_login_enabled: true,
  email_verification: false,
  github_oauth: true,
  github_client_id: 'fixture-client',
  assistant: { enabled: false },
  backend_capabilities: { bounty_public_read: false },
}
async function setup(context, host, persona) {
  await context.addInitScript(() => {
    localStorage.setItem('i18nextLng', 'zhCN')
    localStorage.setItem('lmm:source-consent:v2', 'no')
    window.__reviewTools = new Map()
    Object.defineProperty(document, 'modelContext', {
      configurable: true,
      value: {
        registerTool(tool, { signal }) {
          window.__reviewTools.set(tool.name, tool)
          signal.addEventListener(
            'abort',
            () => {
              if (window.__reviewTools.get(tool.name) === tool) {
                window.__reviewTools.delete(tool.name)
              }
            },
            { once: true }
          )
          return Promise.resolve()
        },
      },
    })
  })
  await context.route('**/*', (route) => {
    const url = new URL(route.request().url())
    if (url.origin !== host) return route.abort('blockedbyclient')
    if (!url.pathname.startsWith('/api/')) return route.continue()
    if (persona) {
      if (url.pathname === '/api/status') {
        return route.fulfill({ json: { success: true, data: status } })
      }
      return route.abort('blockedbyclient')
    }
    if (url.pathname === '/api/user/auth/refresh') {
      return route.fulfill({ status: 401, json: { success: false } })
    }
    let data = []
    if (url.pathname === '/api/setup') data = { status: true }
    if (url.pathname === '/api/status') data = status
    if (url.pathname === '/api/notice') data = ''
    if (
      ['/api/user-agreement', '/api/privacy-policy', '/api/about'].includes(
        url.pathname
      )
    ) {
      data = documentFixture
    }
    return route.fulfill({ json: { success: true, data } })
  })
}
async function snapshot(page, name) {
  await page.evaluate(() => document.fonts.ready)
  // Wait for sheet entry and chapter cross-fades, not the looping sculpture.
  await page.waitForTimeout(1200)
  await page.screenshot({ path: `${output}/${name}.png` })
  const metrics = await page.evaluate(() => ({
    width: innerWidth,
    scrollWidth: document.documentElement.scrollWidth,
    overflowing: [
      ...document.querySelectorAll('main, .console-section-content, form'),
    ]
      .filter(
        (node) =>
          node.clientWidth > 0 && node.scrollWidth > node.clientWidth + 2
      )
      .map((node) => ({
        tag: node.tagName,
        class: node.className,
        width: node.clientWidth,
        scrollWidth: node.scrollWidth,
      })),
  }))
  assert.ok(
    metrics.scrollWidth <= metrics.width + 1,
    `${name}: document overflows horizontally`
  )
  return metrics
}
try {
  for (const [name, width, height, theme, reduced] of [
    ['phone', 390, 844, 'light', false],
    ['compact', 320, 740, 'light', false],
    ['short', 390, 667, 'light', false],
    ['landscape', 844, 390, 'light', false],
    ['tablet', 768, 1024, 'light', false],
    ['desktop', 1440, 1000, 'light', false],
    ['phone-dark', 390, 844, 'dark', false],
    ['reduced', 390, 844, 'light', true],
  ]) {
    if (!selected(name)) continue
    const context = await browser.newContext({
      viewport: { width, height },
      isMobile: width <= 900,
      hasTouch: width <= 900,
      locale: 'zh-CN',
      reducedMotion: reduced ? 'reduce' : 'no-preference',
      serviceWorkers: 'block',
    })
    await setup(context, origin)
    await context.addCookies([
      { name: 'vite-ui-theme', value: theme, url: origin },
    ])
    const page = await context.newPage()
    const errors = []
    page.on('pageerror', (error) => errors.push(error.message))
    try {
      await page.goto(origin, { waitUntil: 'networkidle' })
      await page.waitForFunction(
        () => document.querySelector('[data-film]')?.dataset.ready === 'true'
      )
      await page.waitForTimeout(700)
      const metrics = await snapshot(page, `home-${name}`)
      assert.equal(
        await page.locator('.lmm-home').getAttribute('data-cinema-layout'),
        reduced ? 'stacked' : 'immersive'
      )
      if (!reduced) {
        assert.equal(
          await page.locator('[data-cinema-panel][data-active]').count(),
          1
        )
        for (const chapter of [1, 2, 3, 4, 0]) {
          await page.locator(`[data-cinema-jump="${chapter}"]`).click()
          await page.waitForFunction(
            (value) =>
              document.querySelector('[data-cinema-inner]')?.dataset.chapter ===
              String(value),
            chapter
          )
          assert.equal(
            await page.locator('[data-cinema-panel]:not([inert])').count(),
            1
          )
          if (chapter === 2) await snapshot(page, `home-${name}-tools`)
        }
        if (width <= 900) {
          const session = await context.newCDPSession(page)
          await session.send('Input.dispatchTouchEvent', {
            type: 'touchStart',
            touchPoints: [{ x: width * 0.7, y: height * 0.65 }],
          })
          for (let step = 1; step <= 10; step++) {
            await session.send('Input.dispatchTouchEvent', {
              type: 'touchMove',
              touchPoints: [
                { x: width * 0.7 - step * 3, y: height * (0.65 - 0.04 * step) },
              ],
            })
            await page.waitForTimeout(25)
          }
          await session.send('Input.dispatchTouchEvent', {
            type: 'touchEnd',
            touchPoints: [],
          })
          await page.waitForTimeout(700)
          assert.ok(
            await page.evaluate(
              () =>
                document.querySelector('[data-cinema]').getBoundingClientRect()
                  .top < 0
            ),
            'Touch must still scroll natively'
          )
          await session.detach()
        }
      } else {
        assert.equal(
          await page.locator('[data-cinema-panel]:not([inert])').count(),
          5
        )
      }
      assert.deepEqual(errors, [])
      report.push({ name: `home-${name}`, ok: true, metrics })
    } catch (error) {
      report.push({
        name: `home-${name}`,
        ok: false,
        error: String(error),
        errors,
      })
      await page
        .screenshot({ path: `${output}/failure-${name}.png` })
        .catch(() => {})
    }
    await context.close()
  }
  if (selected('public-pages')) {
    for (const width of [320, 390, 1440]) {
      for (const theme of ['light', 'dark']) {
        const context = await browser.newContext({
          viewport: { width, height: width < 768 ? 844 : 1000 },
          isMobile: width < 768,
          hasTouch: width < 768,
          locale: 'zh-CN',
          reducedMotion: 'reduce',
          serviceWorkers: 'block',
        })
        await context.addCookies([
          { name: 'vite-ui-theme', value: theme, url: origin },
        ])
        await setup(context, origin)
        for (const path of [
          '/sign-in',
          '/sign-up',
          '/forgot-password',
          '/reset',
          '/otp',
          '/401',
          '/403',
          '/404',
          '/500',
          '/503',
          '/security',
          '/terms',
          '/about',
          '/user-agreement',
          '/privacy-policy',
          '/webmcp',
          '/guide',
        ]) {
          const page = await context.newPage()
          const errors = []
          page.on('pageerror', (error) => errors.push(error.message))
          try {
            await page.goto(origin + path, { waitUntil: 'networkidle' })
            await page.waitForTimeout(500)
            const metrics = await snapshot(
              page,
              `${path.slice(1)}-${width}-${theme}`
            )
            if (path === '/about') {
              assert.equal(new URL(page.url()).pathname, '/challenges')
            }
            if (['/user-agreement', '/privacy-policy'].includes(path)) {
              assert.ok(
                (await page.locator('[data-reading-section]').count()) >= 3
              )
              await page
                .getByRole('button', { name: /展开全部|Expand all/ })
                .click()
              assert.equal(
                await page
                  .locator('[data-reading-section]:not([open])')
                  .count(),
                0
              )
            }
            if (path === '/sign-in' || path === '/sign-up') {
              assert.ok((await page.locator('input').count()) > 0)
              const fields = await page
                .locator(
                  '.auth-form-stage input:not([type="hidden"]):not([type="checkbox"])'
                )
                .evaluateAll((nodes) =>
                  nodes
                    .filter((node) => node.getBoundingClientRect().width > 0)
                    .map((node) => ({
                      height: node.getBoundingClientRect().height,
                      font: getComputedStyle(node).fontSize,
                    }))
                )
              assert.ok(
                fields.every(
                  (field) => field.height >= 44 && parseFloat(field.font) >= 16
                ),
                JSON.stringify(fields)
              )
            }
            assert.deepEqual(errors, [])
            report.push({ name: path, width, theme, ok: true, metrics })
          } catch (error) {
            report.push({
              name: path,
              width,
              theme,
              ok: false,
              error: String(error),
              errors,
            })
            await page
              .screenshot({
                path: `${output}/${path.slice(1)}-${width}-${theme}-failed.png`,
              })
              .catch(() => {})
          }
          await page.close()
        }
        await context.close()
      }
    }
  }
  if (consoleOrigin && selected('console')) {
    for (const [persona, width] of [
      ['l1', 390],
      ['admin', 390],
      ['admin', 1440],
    ]) {
      const context = await browser.newContext({
        viewport: { width, height: width === 390 ? 844 : 1000 },
        isMobile: width < 900,
        hasTouch: width < 900,
        locale: 'zh-CN',
      })
      await setup(context, consoleOrigin, persona)
      const paths =
        persona === 'l1'
          ? [
              '/dashboard/overview',
              '/dashboard/models',
              '/keys',
              '/wallet',
              '/profile',
              '/usage-logs/common',
              '/about',
            ]
          : [
              '/system-settings/site/system-info',
              '/system-settings/content/dashboard',
              '/system-settings/auth/basic-auth',
              '/system-settings/billing/currency',
              '/system-settings/operations/behavior',
            ]
      for (const path of paths) {
        const page = await context.newPage()
        const errors = []
        page.on('pageerror', (error) => errors.push(error.message))
        const name = `${persona}-${width}-${path.replaceAll('/', '-')}`
        try {
          await page.goto(
            `${consoleOrigin}${path}?debug_persona=${persona}&console_review=1`,
            { waitUntil: 'domcontentloaded' }
          )
          await page
            .getByTestId('persona-debug-trigger')
            .waitFor({ timeout: 30000 })
          await page.waitForTimeout(1400)
          const metrics = await snapshot(page, name)
          assert.equal(
            await page.getByText(/PERSONA_DEBUG_UNMOCKED_REQUEST/).count(),
            0
          )
          if (path === '/about') {
            assert.ok(
              (await page.locator('[data-reading-section]').count()) >= 3
            )
            const chapter = await page
              .locator('[data-reading-section]')
              .first()
              .boundingBox()
            assert.ok(
              chapter && chapter.height <= 280,
              'Chapter must not repeat full-page padding'
            )
            assert.equal(
              await page
                .locator('.document-section-body .forge-rich-content')
                .count(),
              0
            )
          }
          if (persona === 'admin') {
            const handle = page.locator('.settings-scrub-handle')
            await handle.click()
            await page.locator('.settings-floating-directory').waitFor()
            await snapshot(page, `${name}-directory`)
            await page.keyboard.press('Escape')
            await page
              .locator('.settings-floating-directory')
              .waitFor({ state: 'hidden' })
            if (path.includes('/site/system-info')) {
              await page.waitForFunction(() =>
                window.__reviewTools.has('lmm_settings_form')
              )
              const result = await page.evaluate(async () => {
                const options = { signal: new AbortController().signal }
                const listed = await window.__reviewTools
                  .get('lmm_settings_form')
                  .execute({}, options)
                const form = listed.forms.find((item) =>
                  item.fields.some((field) => field.name === 'SystemName')
                )
                if (!form) {
                  throw new Error('Mounted SystemName form was not registered')
                }
                return window.__reviewTools.get('lmm_settings_preview').execute(
                  {
                    form_id: form.id,
                    changes: { SystemName: 'Mobile review draft' },
                  },
                  options
                )
              })
              assert.equal(result.persisted, false)
              assert.equal(
                await page.locator('input[name="SystemName"]').inputValue(),
                'Mobile review draft'
              )
              await snapshot(page, `${name}-draft`)
            }
          }
          assert.deepEqual(errors, [])
          report.push({ name, ok: true, metrics })
        } catch (error) {
          report.push({ name, ok: false, error: String(error), errors })
          await page
            .screenshot({ path: `${output}/${name}-failed.png` })
            .catch(() => {})
        }
        await page.close()
      }
      await context.close()
    }
  }
} finally {
  await browser.close()
  await writeFile(`${output}/report.json`, JSON.stringify(report, null, 2))
}
console.log(JSON.stringify(report, null, 2))
assert.ok(
  report.length > 0 && report.every((entry) => entry.ok),
  'Some mobile review cases failed; inspect report.json and screenshots'
)
