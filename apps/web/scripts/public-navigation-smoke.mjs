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
await c.addInitScript(() => {
  localStorage.setItem('i18nextLng', 'en')
  localStorage.setItem('lmm:source-consent:v2', 'no')
  document.cookie = 'vite-ui-theme=dark;path=/'
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
try {
  for (const theme of ['dark', 'light']) {
    await c.addCookies([{ name: 'vite-ui-theme', value: theme, url: origin }])
    // Override the initial dark cookie for this navigation.
    await page.addInitScript((theme) => {
      document.cookie = `vite-ui-theme=${theme};path=/`
    }, theme)
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
