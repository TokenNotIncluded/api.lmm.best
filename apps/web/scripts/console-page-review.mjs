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
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import path from 'node:path'

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ?? 'playwright')
const origin = 'http://127.0.0.1:4174'
const output = process.env.CONSOLE_REVIEW_OUTPUT
if (!output) throw new Error('CONSOLE_REVIEW_OUTPUT is required')
await mkdir(output, { recursive: true })
const userRoutes = [
  '/',
  '/temporary-activations',
  '/dashboard/overview',
  '/dashboard/models',
  '/dashboard/flow',
  '/getting-started',
  '/keys',
  '/wallet',
  '/company',
  '/profile',
  '/chat-management',
  '/drawing',
  '/usage-logs/common',
  '/usage-logs/task',
  '/remote-control',
  '/open-source-bounties',
  '/public-relay',
  '/tool-market',
  '/support',
  '/todos',
  '/developers',
  '/guide',
  '/pricing',
  '/status',
  '/scripts',
  '/challenges',
  '/rankings',
  '/workspace',
  '/playground',
]
const adminRoutes = [
  '/channels',
  '/models/metadata',
  '/models/deployments',
  '/users',
  '/redemption-codes',
  '/discount-codes',
  '/red-packets',
  '/subscriptions',
  '/subscriptions/reset',
  '/system-info',
  '/operations/sources',
  '/dashboard/users',
]
for (const category of [
  'site',
  'auth',
  'billing',
  'models',
  'security',
  'content',
  'operations',
]) {
  const source = await readFile(
    new URL(
      `../src/features/system-settings/${category}/section-registry.tsx`,
      import.meta.url
    ),
    'utf8'
  )
  for (const match of source.matchAll(/\bid: '([^']+)'/g)) {
    adminRoutes.push(`/system-settings/${category}/${match[1]}`)
  }
}
const mobileRoutes = [
  '/',
  '/temporary-activations',
  '/profile',
  '/wallet',
  '/keys',
  '/company',
  '/usage-logs/common',
  '/open-source-bounties',
  '/public-relay',
  '/tool-market',
  '/scripts',
  '/challenges',
  '/rankings',
  '/support',
  '/todos',
  '/drawing',
]
const report = []
const browser = await chromium.launch({
  headless: true,
  ...(process.env.PLAYWRIGHT_CHROME_EXECUTABLE
    ? { executablePath: process.env.PLAYWRIGHT_CHROME_EXECUTABLE }
    : {}),
})

async function settle(page) {
  await page.waitForTimeout(800)
  await page.evaluate(() => document.fonts.ready)
}

async function dismissConsent(page) {
  const reject = page.getByRole('button', {
    name: /^(Do not collect|不收集|拒绝收集)$/,
  })
  if (await reject.isVisible()) await reject.click()
}

async function snapshot(page, persona, destination, errors, suffix = '') {
  await settle(page)
  const name = `${persona}${destination.replaceAll('/', '-')}-${page.viewportSize().width}${suffix}`
  const text = await page.locator('body').innerText()
  const dimensions = await page.evaluate(() => ({
    width: innerWidth,
    scroll: document.documentElement.scrollWidth,
  }))
  const title = await page.locator('h1:visible,h2:visible').allTextContents()
  const crash =
    /Oops|Internal Server Error|Something went wrong|PERSONA_DEBUG_UNMOCKED_REQUEST|Invalid language tag|invalid language tag|糟糕！出错了/.test(
      text
    )
  const loadErrors =
    text.match(
      /无法加载企业资料|待办加载失败|获取启用模型失败|加载 playground 模型失败|无法加载符合条件的订阅/g
    ) ?? []
  const errorToasts = await page
    .locator('[data-sonner-toast][data-type="error"]:visible')
    .allTextContents()
  const entry = {
    persona,
    path: destination,
    url: page.url(),
    title,
    screenshot: `${name}.png`,
    dimensions,
    crash,
    errors: [...errors, ...loadErrors, ...errorToasts],
    text: text.slice(0, 16000),
  }
  await page.screenshot({
    path: path.join(output, entry.screenshot),
    fullPage: true,
    animations: 'disabled',
  })
  report.push(entry)
  console.log(
    JSON.stringify({
      path: destination,
      width: dimensions.width,
      suffix,
      title,
      crash,
      errors: errors.slice(0, 3),
    })
  )
  await writeFile(
    path.join(output, 'report.json'),
    JSON.stringify(report, null, 2)
  )
}

try {
  for (const [persona, routes, width] of [
    ['l1', userRoutes, 1440],
    ['admin', adminRoutes, 1440],
    ['l1', mobileRoutes, 390],
  ]) {
    const context = await browser.newContext({
      viewport: { width, height: width === 390 ? 844 : 1000 },
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
              logo: '/logo.png',
              assistant: { enabled: true },
              announcements_enabled: false,
            },
          },
        })
      }
      if (url.pathname.startsWith('/api/')) {
        errors.push(`NETWORK: ${route.request().method()} ${url.pathname}`)
        return route.abort('blockedbyclient')
      }
      return route.continue()
    })
    const page = await context.newPage()
    page.setDefaultTimeout(10000)
    page.on('pageerror', (error) => errors.push(String(error.stack ?? error)))
    page.on('console', (message) => {
      if (message.type() === 'error') {
        errors.push(message.text())
      }
    })
    await page.goto(
      `${origin}${routes[0]}?debug_persona=${persona}&console_review=1`,
      { waitUntil: 'domcontentloaded' }
    )
    await page.getByTestId('persona-debug-trigger').waitFor()
    await settle(page)
    await dismissConsent(page)
    for (const destination of routes) {
      errors.length = 0
      try {
        await page.evaluate((to) => {
          history.pushState({}, '', to)
          window.dispatchEvent(new PopStateEvent('popstate'))
        }, `${destination}?debug_persona=${persona}&console_review=1`)
        await settle(page)
        await snapshot(page, persona, destination, errors)
        if (destination === '/profile') {
          const tabs = page.locator('.console-page-tabs > [role=tablist]')
          const all = tabs.getByRole('tab')
          for (let index = 1; index < (await all.count()); index += 1) {
            await all.nth(index).click()
            await snapshot(page, persona, destination, errors, `-tab-${index}`)
          }
        }
        if (destination === '/temporary-activations') {
          // Read both order views. Never click buy, cancel, refund, or history deletion.
          const historyTab = page
            .locator('.console-sms-orders [role=tab]')
            .nth(1)
          await historyTab.click()
          await snapshot(page, persona, destination, errors, '-history')
          const emailTab = page.getByRole('tab', {
            name: /^(Email address|邮箱地址|电子邮箱|电子邮件地址|邮箱接码)$/,
          })
          if (await emailTab.count()) {
            await emailTab.click()
            await snapshot(page, persona, destination, errors, '-email')
          }
        }
        if (destination === '/wallet') {
          for (const id of [
            'trust-level',
            'subscription-plans',
            'referral-program',
          ]) {
            const summary = page.locator(`#${id} > summary`)
            if (await summary.count()) {
              await summary.click()
              await summary.scrollIntoViewIfNeeded()
              await snapshot(page, persona, destination, errors, `-${id}`)
              await summary.click()
            }
          }
        }
        if (['/usage-logs/common', '/channels'].includes(destination)) {
          const button = page.locator('.console-toolbar button[aria-controls]')
          if (await button.count()) {
            await button.click()
            await snapshot(page, persona, destination, errors, '-filters')
            await button.click()
          }
        }
        if (
          width === 1440 &&
          [
            '/temporary-activations',
            '/keys',
            '/wallet',
            '/profile',
            '/channels',
          ].includes(destination)
        ) {
          await page.evaluate(() =>
            document.documentElement.classList.add('dark')
          )
          await snapshot(page, persona, destination, errors, '-dark')
          await page.evaluate(() =>
            document.documentElement.classList.remove('dark')
          )
        }
      } catch (error) {
        report.push({
          persona,
          path: destination,
          width,
          error: String(error),
          errors: [...errors],
        })
        await writeFile(
          path.join(output, 'report.json'),
          JSON.stringify(report, null, 2)
        )
      }
    }
    await context.close()
  }
} finally {
  await writeFile(
    path.join(output, 'report.json'),
    JSON.stringify(report, null, 2)
  )
  await browser.close()
}
const failures = report.filter(
  (entry) =>
    entry.error ||
    entry.crash ||
    entry.errors.length ||
    entry.dimensions.scroll > entry.dimensions.width + 1
)
console.log(
  `Captured ${report.length} route/view states; failures: ${failures.length}`
)
assert.equal(
  failures.length,
  0,
  JSON.stringify(
    failures.map(({ path, error, errors, crash }) => ({
      path,
      error,
      errors,
      crash,
    }))
  )
)
