/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ?? 'playwright')
const origin = process.env.PUBLIC_NAV_ORIGIN ?? 'http://127.0.0.1:4189'
assert.ok(
  ['127.0.0.1', 'localhost', '[::1]'].includes(new URL(origin).hostname),
  'Local fixtures only'
)
const output =
  process.env.PUBLIC_NAV_SCREENSHOTS ?? 'output/playwright/public-navigation'
await mkdir(output, { recursive: true })
const b = await chromium.launch({
  headless: true,
  executablePath: process.env.CHROME_PATH || undefined,
})
const c = await b.newContext({
  viewport: { width: 1235, height: 867 },
  reducedMotion: 'reduce',
})
async function configureContext(c, authenticated = true) {
  await c.addCookies([{ name: 'vite-ui-theme', value: 'dark', url: origin }])
  await c.addInitScript(() => {
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('lmm:source-consent:v2', 'no')
  })
  await c.route('**/*', (r) => {
    const u = new URL(r.request().url())
    if (u.origin !== origin) return r.abort('blockedbyclient')
    if (!u.pathname.startsWith('/api/')) return r.continue()
    const p = new URL(r.request().url()).pathname
    let j = { success: true, data: {} }
    if (p === '/api/setup') j.data = { status: true }
    if (p === '/api/status') {
      j.data = {
        system_name: 'LMM Forge',
        HeaderNavModules: { pricing: { enabled: true, requireAuth: false } },
        assistant: { enabled: false },
      }
    }
    if (p === '/api/notice') j.data = ''
    if (p === '/api/pricing') {
      j = {
        success: true,
        data: [
          {
            id: 1,
            model_name: 'local-navigation-fixture',
            vendor_id: 1,
            quota_type: 0,
            model_ratio: 1,
            completion_ratio: 2,
            enable_groups: ['default'],
            supported_endpoint_types: ['openai'],
          },
        ],
        vendors: [{ id: 1, name: 'Local provider' }],
        group_ratio: { default: 1 },
        usable_group: { default: { desc: 'Default', ratio: 1 } },
        supported_endpoint: {},
        auto_groups: [],
      }
    }
    if (p === '/api/user/auth/refresh') {
      if (!authenticated) {
        return r.fulfill({
          status: 401,
          json: { success: false, message: 'Signed-out local fixture' },
        })
      }
      j = {
        success: true,
        data: {
          access_token: 'fictional-local-token',
          token_type: 'Bearer',
          access_expires_at: Math.floor(Date.now() / 1000) + 3600,
          user: {
            id: 1,
            username: 'fixture',
            role: 1,
            developer_access_granted: true,
          },
          session: {
            sid: 'fixture',
            current: true,
            login_method: 'fixture',
            ip: '127.0.0.1',
            user_agent: 'playwright',
            created_at: 1,
            last_active_at: 1,
            expires_at: Math.floor(Date.now() / 1000) + 3600,
          },
        },
      }
    }
    return r.fulfill({ json: j })
  })
}
await configureContext(c)

const page = await c.newPage()
page.setDefaultTimeout(15000)
const errors = []
page.on('pageerror', (e) => errors.push(e.message))
const toggle = page.getByRole('button', { name: 'Toggle navigation menu' })
const dialog = page.locator('#public-mobile-navigation')
const model = page.getByRole('heading', {
  name: 'local-navigation-fixture',
  exact: true,
})
async function assertScrollPosition(page, scrollY, hash, label) {
  const position = await page.evaluate(() => ({
    y: window.scrollY,
    hash: location.hash,
  }))
  assert.ok(
    Math.abs(position.y - scrollY) <= 1,
    `${label}: scrollY ${position.y}, expected ${scrollY}`
  )
  assert.equal(position.hash, hash, `${label}: keeps the anchor`)
}

async function checkScrolledMenu(page, label, width, height) {
  const toggle = page.getByRole('button', { name: 'Toggle navigation menu' })
  const dialog = page.locator('#public-mobile-navigation')
  for (const [path, anchor] of [
    ['/', 'lmm-connect-title'],
    ['/guide', 'guide-troubleshooting'],
  ]) {
    const routeLabel = `${label}-${path === '/' ? 'home' : 'guide'}`
    await page.setViewportSize({ width, height })
    await page.goto(`${origin}${path}`)
    await page.locator(`a[href="#${anchor}"]`).click()
    assert.equal(
      await page
        .locator('html')
        .evaluate((el) => el.classList.contains('dark')),
      label.startsWith('dark')
    )
    await page.waitForFunction(() => window.scrollY > 500)
    await page.waitForTimeout(750) // finish the page's smooth anchor scroll
    const before = await page.evaluate(() => ({
      scrollY: window.scrollY,
      hash: location.hash,
      rootOverflow: document.documentElement.style.overflow,
      bodyOverflow: document.body.style.overflow,
    }))
    assert.equal(before.hash, `#${anchor}`)
    for (const close of ['button', 'Escape']) {
      await toggle.click()
      await page
        .locator('#public-mobile-navigation[aria-hidden="false"]')
        .waitFor()
      await page.waitForTimeout(750) // finish the staggered menu entrance
      await assertScrollPosition(
        page,
        before.scrollY,
        before.hash,
        `${routeLabel}/${close} open`
      )
      assert.equal(
        await page.locator('html').evaluate((el) => el.style.overflow),
        'hidden'
      )
      assert.equal(
        await page.locator('body').evaluate((el) => el.style.overflow),
        before.bodyOverflow
      )
      assert.ok(
        await toggle.evaluate((el) => {
          const rect = el.getBoundingClientRect()
          return (
            rect.top >= 0 &&
            rect.bottom <= innerHeight &&
            el.contains(
              document.elementFromPoint(
                rect.x + rect.width / 2,
                rect.y + rect.height / 2
              )
            )
          )
        }),
        `${routeLabel}: visible close button is hit-testable`
      )
      const menuSize = await dialog.evaluate((el) => ({
        height: el.clientHeight,
        content: el.scrollHeight,
      }))
      if (height <= 600) {
        assert.ok(
          menuSize.content > menuSize.height,
          'short viewport exercises menu overflow'
        )
      }
      if (menuSize.content > menuSize.height) {
        await dialog.hover()
        await page.mouse.wheel(0, 600)
        await page.waitForFunction(
          () =>
            document.querySelector('#public-mobile-navigation').scrollTop > 0
        )
      }
      await assertScrollPosition(
        page,
        before.scrollY,
        before.hash,
        `${routeLabel} menu scroll`
      )
      await page.screenshot({
        path: `${output}/${routeLabel}-${close}-open.png`,
      })
      if (close === 'button') await toggle.click()
      else await page.keyboard.press('Escape')
      await page
        .locator('#public-mobile-navigation[aria-hidden="true"]')
        .waitFor()
      await assertScrollPosition(
        page,
        before.scrollY,
        before.hash,
        `${routeLabel}/${close} close`
      )
      assert.equal(
        await page.locator('html').evaluate((el) => el.style.overflow),
        before.rootOverflow
      )
      if (close === 'Escape') {
        assert.ok(await toggle.evaluate((el) => el === document.activeElement))
      }
    }
    await toggle.click()
    await page
      .locator('#public-mobile-navigation[aria-hidden="false"]')
      .waitFor()
    await page.setViewportSize({ width: 1440, height })
    await page
      .locator('#public-mobile-navigation[aria-hidden="true"]')
      .waitFor()
    assert.equal(
      await page.locator('html').evaluate((el) => el.style.overflow),
      before.rootOverflow
    )
    assert.equal(await toggle.isVisible(), false)
    await page.setViewportSize({ width, height })
    assert.equal(await toggle.getAttribute('aria-expanded'), 'false')
    await toggle.click()
    const destination = path === '/' ? '/guide' : '/'
    await dialog
      .getByRole('link', { name: path === '/' ? 'Guide' : 'Home', exact: true })
      .click()
    await page.waitForURL(`${origin}${destination}`)
    await page
      .locator('#public-mobile-navigation[aria-hidden="true"]')
      .waitFor()
    assert.equal(
      await page.locator('html').evaluate((el) => el.style.overflow),
      before.rootOverflow
    )
    console.log(
      `PASS ${routeLabel}: scrolled anchor, visible close, X/Escape, repeated open, menu scroll, resize cleanup, navigation`
    )
  }
}

try {
  for (const theme of ['dark', 'light']) {
    await c.addCookies([{ name: 'vite-ui-theme', value: theme, url: origin }])
    for (const width of [1235, 390, 1440]) {
      await page.setViewportSize({ width, height: 1000 })
      await page.goto(`${origin}/pricing`)
      await model.waitFor()
      const consent = page.getByRole('button', {
        name: 'Do not collect',
        exact: true,
      })
      if (await consent.isVisible()) await consent.click()
      assert.equal(
        await page
          .locator('html')
          .evaluate((el) => el.classList.contains('dark')),
        theme === 'dark'
      )
      await page.screenshot({
        path: `${output}/${theme}-${width}-closed.png`,
        fullPage: true,
      })
      if (width === 1440) {
        assert.equal(await toggle.isVisible(), false)
        continue
      }
      await toggle.click()
      await page
        .locator('#public-mobile-navigation[aria-hidden="false"]')
        .waitFor()
      await page
        .getByRole('dialog', { name: 'Header navigation' })
        .getByRole('link', { name: 'Security', exact: true })
        .waitFor()
      await page.waitForTimeout(1000) // staggered navigation transition
      const ownsSearch = await page
        .locator('input')
        .first()
        .evaluate((el) => {
          const r = el.getBoundingClientRect()
          const hit = document.elementFromPoint(
            r.x + r.width / 2,
            r.y + r.height / 2
          )
          return Boolean(hit?.closest('#public-mobile-navigation'))
        })
      assert.ok(
        ownsSearch,
        `${theme}/${width}: navigation must cover pricing search`
      )
      await page.screenshot({ path: `${output}/${theme}-${width}-open.png` })
      await page.keyboard.press('Escape')
      await page
        .locator('#public-mobile-navigation[aria-hidden="true"]')
        .waitFor()
      assert.ok(
        await toggle.evaluate((el) => el === document.activeElement),
        'Escape restores toggle focus'
      )
      await model.waitFor()
      await toggle.click()
      await dialog.getByRole('link', { name: 'Home', exact: true }).click()
      await page.waitForURL(`${origin}/`)
      await page
        .locator('#public-mobile-navigation[aria-hidden="true"]')
        .waitFor()
      console.log(
        `PASS ${theme}/${width}: menu layering, Escape, route close, model content`
      )
    }
  }
  const publicContext = await b.newContext({ reducedMotion: 'reduce' })
  try {
    await configureContext(publicContext, false)
    const publicPage = await publicContext.newPage()
    publicPage.on('pageerror', (e) => errors.push(e.message))
    for (const theme of ['dark', 'light']) {
      await publicContext.addCookies([
        { name: 'vite-ui-theme', value: theme, url: origin },
      ])
      await checkScrolledMenu(publicPage, `${theme}-1180`, 1180, 757)
    }
  } finally {
    await publicContext.close()
  }
  // Real Chromium rendering with mobile/touch emulation, not physical-device coverage.
  const mobile = await b.newContext({
    viewport: { width: 390, height: 600 },
    isMobile: true,
    hasTouch: true,
    deviceScaleFactor: 3,
    reducedMotion: 'reduce',
  })
  try {
    await configureContext(mobile, false)
    const mobilePage = await mobile.newPage()
    mobilePage.on('pageerror', (e) => errors.push(e.message))
    await checkScrolledMenu(mobilePage, 'dark-mobile-touch-390', 390, 600)
  } finally {
    await mobile.close()
  }
  await page.setViewportSize({ width: 1440, height: 1000 })
  let releasePricing
  const pricingGate = new Promise((resolve) => {
    releasePricing = resolve
  })
  await page.route('**/api/pricing', async (route) => {
    await pricingGate
    await route.fallback()
  })
  await page.goto(`${origin}/pricing`)
  await page.locator('[data-slot="skeleton"]').first().waitFor()
  await page.screenshot({ path: `${output}/api-loading.png`, fullPage: true })
  releasePricing()
  await model.waitFor()
  console.log('PASS pending source displays a skeleton and then models')
  await page.route('**/api/pricing', (route) =>
    route.fulfill({
      status: 503,
      json: { success: false, message: 'Local fixture unavailable' },
    })
  )
  await page.goto(`${origin}/pricing`)
  await page
    .getByText('Failed to load enabled models', { exact: true })
    .waitFor()
  await page.screenshot({ path: `${output}/api-error.png`, fullPage: true })
  assert.equal(errors.length, 0, errors.join('\n'))
  console.log('PASS source failure is visible, no page errors')
} finally {
  await b.close()
}
