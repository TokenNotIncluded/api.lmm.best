/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import { createRequire } from 'node:module'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const require = createRequire(import.meta.url)
// Keep the system/global Playwright fallback; this repository intentionally
// does not add a Playwright dependency for this read-only smoke check.
const playwrightEntry = (() => {
  try {
    return require.resolve('playwright')
  } catch {
    const nodePrefix = path.resolve(path.dirname(process.execPath), '../lib')
    try {
      return require.resolve('playwright', { paths: [nodePrefix] })
    } catch (error) {
      try {
        return require.resolve('playwright', {
          paths: ['/usr/lib/node_modules'],
        })
      } catch {
        throw new Error(
          'Playwright is not available locally; use the machine-provided Playwright to run this smoke check (no repository dependency is added).',
          { cause: error }
        )
      }
    }
  }
})()
const { chromium } = (await import(pathToFileURL(playwrightEntry).href)).default

const scriptDirectory = path.dirname(fileURLToPath(import.meta.url))
const viewportMatch = /^([1-9]\d*)x([1-9]\d*)$/.exec(
  process.env.DASHBOARD_SMOKE_VIEWPORT ?? ''
)
const viewport = viewportMatch
  ? { width: Number(viewportMatch[1]), height: Number(viewportMatch[2]) }
  : { width: 1440, height: 900 }
const outputSuffix = process.env.DASHBOARD_SMOKE_OUTPUT_SUFFIX
  ? `-${process.env.DASHBOARD_SMOKE_OUTPUT_SUFFIX}`
  : ''
const outputDirectory = path.resolve(
  scriptDirectory,
  `../test-results/read-only-dashboard-smoke${outputSuffix}`
)
const now = Math.floor(Date.now() / 1000)
const baseUrl = process.env.DASHBOARD_SMOKE_URL ?? 'http://127.0.0.1:4174'
const baseOrigin = new URL(baseUrl).origin

const authFixture = {
  access_token: 'smoke-dashboard-token',
  token_type: 'Bearer',
  access_expires_at: now + 3600,
  user: {
    id: 1,
    username: 'readonly_smoke_user',
    role: 10,
    display_name: 'Read-only Smoke User',
    developer_access_granted: true,
    onboarding: {
      activation_complete: true,
      credential_complete: true,
      first_request_complete: true,
      stage: 'complete',
    },
  },
  session: {
    sid: 'smoke-dashboard-session',
    current: true,
    login_method: 'smoke',
    ip: '127.0.0.1',
    user_agent: 'playwright',
    created_at: now,
    last_active_at: now,
    expires_at: now + 7200,
  },
}

const statusFixture = {
  version: 'smoke-fixture',
  system_name: 'lmm.best',
  demo_site_enabled: false,
  logo: '/logo.png',
  display_token_stat_enabled: true,
  display_in_currency: false,
  self_use_mode_enabled: false,
}

const setupFixture = {
  success: true,
  data: {
    status: true,
    root_init: true,
    SelfUseModeEnabled: false,
    DemoSiteEnabled: false,
  },
  message: 'smoke fixture',
}

const quotaFixture = [
  {
    id: 1,
    user_id: 1,
    username: 'readonly_smoke_user',
    model_name: 'gpt-4o',
    created_at: now,
    token_used: 1200,
    count: 12,
    quota: 2000,
  },
  {
    id: 2,
    user_id: 1,
    username: 'readonly_smoke_user',
    model_name: 'gpt-4.1',
    created_at: now + 3600,
    token_used: 800,
    count: 8,
    quota: 2000,
  },
]

const flowFixture = [
  {
    user_id: 1,
    username: 'readonly_smoke_user',
    node_name: 'node-a',
    use_group: 'default',
    token_id: 901,
    token_name: 'smoke-token-901',
    channel_id: 11,
    channel_name: 'playwright',
    model_name: 'gpt-4o',
    token_used: 1200,
    count: 12,
    quota: 2000,
  },
]

function tokenPagePayload() {
  return {
    success: true,
    data: {
      items: [],
      total: 0,
      page: 1,
      page_size: 10,
    },
    message: 'smoke fixture',
  }
}

function jsonFixture(pathname) {
  if (pathname === '/api/user/auth/refresh') {
    return { success: true, data: authFixture, message: 'smoke fixture' }
  }
  if (pathname === '/api/setup') {
    return setupFixture
  }
  if (pathname === '/api/status') {
    return { success: true, data: statusFixture, message: 'smoke fixture' }
  }
  if (pathname === '/api/user/self') {
    return { success: true, data: authFixture.user, message: 'smoke fixture' }
  }
  if (pathname === '/api/user/groups') {
    return { success: true, data: {}, message: 'smoke fixture' }
  }
  if (pathname === '/api/notice') {
    return { success: true, data: '', message: 'smoke fixture' }
  }
  if (pathname === '/api/open-source-bounties/tips/received') {
    return { success: true, data: [], message: 'smoke fixture' }
  }
  if (pathname === '/api/data' || pathname === '/api/data/self') {
    return { success: true, data: quotaFixture, message: 'smoke fixture' }
  }
  if (pathname === '/api/data/users') {
    return { success: true, data: quotaFixture, message: 'smoke fixture' }
  }
  if (pathname === '/api/data/flow' || pathname === '/api/data/flow/self') {
    return { success: true, data: flowFixture, message: 'smoke fixture' }
  }
  if (pathname === '/api/uptime/status') {
    return {
      success: true,
      data: [
        {
          categoryName: 'smoke',
          monitors: [],
        },
      ],
      message: 'smoke fixture',
    }
  }
  if (pathname === '/api/perf-metrics/summary') {
    return {
      success: true,
      data: { models: [] },
      message: 'smoke fixture',
    }
  }
  if (pathname === '/api/user/models') {
    return {
      success: true,
      data: ['gpt-4o', 'gpt-4.1'],
      message: 'smoke fixture',
    }
  }
  if (pathname === '/api/todos') {
    return {
      success: true,
      data: {
        items: [],
        page: 1,
        page_size: 10,
        total: 0,
        category: 'all',
        unread_count: 0,
        total_unread_count: 0,
        unread_by_category: {},
        categories: [],
      },
      message: 'smoke fixture',
    }
  }
  if (pathname === '/api/release-notes/latest') {
    return { success: true, data: null, message: 'smoke fixture' }
  }
  if (pathname.startsWith('/api/token')) {
    return tokenPagePayload()
  }
  throw new Error(`Missing API fixture for ${pathname}`)
}

function assertNoErrors({
  pageErrors,
  failedRequests,
  forbiddenApiRequests,
  missingFixturePaths,
  bodyText,
}) {
  assert.equal(pageErrors.length, 0, `page errors: ${pageErrors.join('; ')}`)
  assert.equal(
    missingFixturePaths.length,
    0,
    `missing API fixtures: ${missingFixturePaths.join(', ')}`
  )
  assert.equal(
    failedRequests.length,
    0,
    `failed requests (including static resources): ${failedRequests.join(', ')}`
  )
  assert.equal(
    forbiddenApiRequests.length,
    0,
    `forbidden API requests: ${forbiddenApiRequests.join(', ')}`
  )
  assert.equal(
    /(\b401\b|\b403\b|\b500\b|\b501\b|\b502\b|\b503\b|\b504\b|Gateway Timeout|Failed to)/.test(
      bodyText
    ),
    false,
    'page rendered error-like status or message'
  )
}

async function installApiFixtures(page) {
  const missingFixturePaths = new Set()
  await page.route('**/api/**', async (route) => {
    const pathname = new URL(route.request().url()).pathname
    try {
      await route.fulfill({
        contentType: 'application/json',
        json: jsonFixture(pathname),
      })
    } catch {
      missingFixturePaths.add(pathname)
      await route.abort('failed')
    }
  })
  return missingFixturePaths
}

function normalizeDashboardPath(pathname) {
  if (!pathname || pathname === '/dashboard') return '/dashboard'
  if (pathname.startsWith('/dashboard/')) return pathname
  if (pathname.startsWith('/')) return pathname
  return `/dashboard/${pathname}`
}

function isAllowedReadOnlyApiRequest(request, requestUrl) {
  return (
    request.method() === 'POST' &&
    requestUrl.origin === baseOrigin &&
    requestUrl.pathname === '/api/user/auth/refresh'
  )
}

function sectionTag(pathname) {
  return pathname === '/dashboard'
    ? 'dashboard-overview'
    : pathname.slice(1).replaceAll('/', '-')
}

async function navigateAndCheck(page, pathname, metrics) {
  metrics.pageErrors.length = 0
  metrics.failedRequests.length = 0
  metrics.forbiddenApiRequests.length = 0
  const url = new URL(normalizeDashboardPath(pathname), baseUrl).toString()

  await page.goto(url, {
    waitUntil: 'networkidle',
    timeout: 60_000,
  })
  await page.waitForTimeout(350)

  await assertCurrentDashboardNavigation(page, metrics)

  await page.screenshot({
    path: path.join(outputDirectory, `${sectionTag(pathname)}.png`),
    fullPage: true,
  })
}

async function assertCurrentDashboardNavigation(page, metrics) {
  const bodyText = await page
    .locator('body')
    .innerText()
    .catch(() => '')
  assert.equal(
    page.url().startsWith(`${baseUrl}/dashboard`),
    true,
    `navigation redirected outside dashboard: ${page.url()}`
  )
  assertNoErrors({
    pageErrors: metrics.pageErrors,
    failedRequests: metrics.failedRequests,
    forbiddenApiRequests: metrics.forbiddenApiRequests,
    missingFixturePaths: [...metrics.missingFixturePaths],
    bodyText,
  })
}

async function collectDashboardSections(page, metrics) {
  await page.goto(`${baseUrl}/dashboard`, {
    waitUntil: 'networkidle',
    timeout: 60_000,
  })
  await page.waitForTimeout(350)
  await assertCurrentDashboardNavigation(page, metrics)

  const links = await page.locator('a[href^="/dashboard/"]').elementHandles()
  const discovered = new Set(['/dashboard'])
  for (const anchor of links) {
    const href = await anchor.getAttribute('href')
    if (!href) continue
    const pathname = new URL(href, baseUrl).pathname
    if (
      pathname === '/dashboard/overview' ||
      pathname === '/dashboard/models' ||
      pathname === '/dashboard/flow' ||
      pathname === '/dashboard/users'
    ) {
      discovered.add(pathname)
    }
  }
  return [
    '/dashboard',
    '/dashboard/overview',
    '/dashboard/models',
    '/dashboard/flow',
    '/dashboard/users',
    ...[...discovered].filter(
      (sectionPath) =>
        !/^\/dashboard(\/(overview|models|flow|users))?$/.test(sectionPath)
    ),
  ]
}

async function buildContextHooks(page, metrics) {
  page.on('request', (request) => {
    const requestUrl = new URL(request.url())
    const isWrite = !['GET', 'HEAD', 'OPTIONS'].includes(request.method())
    // Every origin is observed; same-origin writes on any path and cross-origin
    // API writes are forbidden (the refresh call below is the only exception).
    if (isWrite) {
      if (isAllowedReadOnlyApiRequest(request, requestUrl)) return
      metrics.forbiddenApiRequests.push(
        `${request.method()} ${requestUrl.origin}${requestUrl.pathname}`
      )
    }
  })

  page.on('requestfailed', (request) => {
    const requestUrl = new URL(request.url())
    metrics.failedRequests.push(
      `${request.method()} ${requestUrl.origin}${requestUrl.pathname}`
    )
  })

  page.on('pageerror', (error) => {
    metrics.pageErrors.push(error.message)
  })
}

await mkdir(outputDirectory, { recursive: true })

let browser
let context
try {
  browser = await chromium.launch({ headless: true })
  context = await browser.newContext({
    viewport,
    recordHar: {
      path: path.join(outputDirectory, 'dashboard-network.har'),
      mode: 'full',
    },
  })
  const page = await context.newPage()
  const metrics = {
    failedRequests: [],
    pageErrors: [],
    forbiddenApiRequests: [],
    missingFixturePaths: new Set(),
  }

  metrics.missingFixturePaths = await installApiFixtures(page)
  await buildContextHooks(page, metrics)

  const paths = await collectDashboardSections(page, metrics)
  const uniquePaths = [...new Set(paths)]

  for (const pathname of uniquePaths) {
    await navigateAndCheck(page, pathname, metrics)
  }

  console.log(
    `dashboard smoke: PASS (${uniquePaths.length} routes) ${uniquePaths.join(', ')}`
  )
} finally {
  try {
    await context?.close()
  } finally {
    await browser?.close()
  }
}
