/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import path from 'node:path'

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ?? 'playwright')
const origin = 'http://127.0.0.1:4174'
const output = process.env.CONSOLE_REVIEW_OUTPUT
if (!output) throw new Error('CONSOLE_REVIEW_OUTPUT is required')
await mkdir(output, { recursive: true })
const userRoutes = [
  '/temporary-activations', '/dashboard/overview', '/dashboard/models',
  '/dashboard/flow', '/getting-started', '/keys', '/wallet', '/company',
  '/profile', '/chat-management', '/drawing', '/usage-logs/common',
  '/usage-logs/task', '/remote-control', '/open-source-bounties',
  '/public-relay', '/tool-market', '/support', '/todos', '/developers',
  '/guide', '/pricing', '/status', '/scripts', '/challenges', '/rankings',
  '/workspace', '/playground',
]
const adminRoutes = [
  '/channels', '/models/metadata', '/models/deployments', '/users',
  '/redemption-codes', '/discount-codes', '/red-packets', '/subscriptions',
  '/subscriptions/reset', '/system-info', '/operations/sources', '/dashboard/users',
]
for (const category of ['site', 'auth', 'billing', 'models', 'security', 'content', 'operations']) {
  const source = await readFile(new URL(`../src/features/system-settings/${category}/section-registry.tsx`, import.meta.url), 'utf8')
  for (const match of source.matchAll(/\bid: '([^']+)'/g)) {
    adminRoutes.push(`/system-settings/${category}/${match[1]}`)
  }
}
const report = []
const browser = await chromium.launch({ headless: true })
try {
  for (const [persona, routes] of [['l1', userRoutes], ['admin', adminRoutes]]) {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, locale: 'en-US', serviceWorkers: 'block' })
    await context.addInitScript(() => {
      localStorage.setItem('i18nextLng', 'en')
      localStorage.setItem('lmm:source-consent:v2', 'no')
    })
    const failures = []
    await context.route('**/*', async (route) => {
      const url = new URL(route.request().url())
      if (url.origin !== origin) return route.abort('blockedbyclient')
      if (url.pathname === '/api/status') return route.fulfill({ json: { success: true, data: { system_name: 'LMM Best', logo: '/logo.png', assistant: { enabled: true }, announcements_enabled: false } } })
      if (url.pathname.startsWith('/api/')) {
        failures.push(`NETWORK: ${route.request().method()} ${url.pathname}`)
        return route.abort('blockedbyclient')
      }
      return route.continue()
    })
    const page = await context.newPage()
    page.setDefaultTimeout(10000)
    page.on('pageerror', (error) => failures.push(String(error.stack ?? error)))
    page.on('console', (message) => {
      if (message.type() === 'error') failures.push(message.text())
    })
    const activate = async () => {
      await page.goto(origin, { waitUntil: 'domcontentloaded' })
      await page.getByTestId('persona-debug-trigger').click()
      await page.getByTestId(`persona-debug-option-${persona}`).click()
      await page.waitForURL(/\/dashboard/)
      await page.keyboard.press('Escape')
      await page.getByTestId('persona-debug-panel').waitFor({ state: 'hidden' })
    }
    await activate()
    for (const destination of routes) {
      failures.length = 0
      const name = `${persona}${destination.replaceAll('/', '-')}`
      let text = ''
      try {
        await page.evaluate((to) => {
          history.pushState({}, '', to)
          window.dispatchEvent(new PopStateEvent('popstate'))
        }, destination)
        await page.waitForTimeout(900)
        await page.evaluate(() => document.fonts.ready)
        text = await page.locator('body').innerText()
        const dimensions = await page.evaluate(() => ({ width: innerWidth, scroll: document.documentElement.scrollWidth }))
        const title = await page.locator('h1,h2').allTextContents()
        const crash = /Oops|Internal Server Error|Something went wrong|PERSONA_DEBUG_UNMOCKED_REQUEST/.test(text)
        await page.screenshot({ path: path.join(output, `${name}.png`), fullPage: true, animations: 'disabled' })
        report.push({ persona, path: destination, url: page.url(), title, screenshot: `${name}.png`, dimensions, crash, errors: [...failures], text: text.slice(0, 12000) })
        console.log(JSON.stringify({ path: destination, title, crash, errors: failures.slice(0, 3) }))
      } catch (error) {
        report.push({ persona, path: destination, error: String(error), errors: [...failures], text })
        await activate()
      }
      await writeFile(path.join(output, 'report.json'), JSON.stringify(report, null, 2))
    }
    await context.close()
  }
} finally {
  await writeFile(path.join(output, 'report.json'), JSON.stringify(report, null, 2))
  await browser.close()
}
console.log(`Reviewed ${report.length} console routes`)
