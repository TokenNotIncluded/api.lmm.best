/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// Synthetic browser acceptance. All API requests are intercepted; external
// origins are blocked. No user account or payment provider is contacted.
import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const { chromium } = (
  await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE).href)
).default
const base = new URL(process.env.STORE_REVIEW_URL || 'http://127.0.0.1:4183')
assert.equal(base.protocol, 'http:')
assert.equal(base.hostname, '127.0.0.1')
const output =
  process.env.STORE_REVIEW_OUTPUT || path.resolve('output/store-refund-login')
await mkdir(output, { recursive: true })
const now = Math.floor(Date.now() / 1000)
const token = 'a'.repeat(43)
const pickup = `/store/claim/${token}?from=receipt#keep-return-fragment`
const bundle = (id) => ({
  access_token: `synthetic-user-${id}`,
  token_type: 'Bearer',
  access_expires_at: now + 3600,
  user: {
    id,
    role: 1,
    username: `synthetic-${id}`,
    quota: 5000000,
    developer_access_granted: true,
    onboarding: {
      activation_complete: true,
      credential_complete: true,
      first_request_complete: true,
      stage: 'complete',
    },
  },
  session: {
    sid: `synthetic-${id}`,
    current: true,
    login_method: 'fixture',
    ip: '127.0.0.1',
    user_agent: 'fixture',
    created_at: now,
    last_active_at: now,
    expires_at: now + 7200,
  },
})
const results = []
const browser = await chromium.launch({ headless: true })
try {
  for (const [width, height, colorScheme] of [
    [1440, 1000, 'light'],
    [390, 844, 'dark'],
  ]) {
    const context = await browser.newContext({
      viewport: { width, height },
      colorScheme,
      locale: 'en-US',
      reducedMotion: 'reduce',
      serviceWorkers: 'block',
    })
    await context.addInitScript(() => localStorage.setItem('i18nextLng', 'en'))
    await context.addCookies([
      { name: 'vite-ui-theme', value: colorScheme, url: base.origin },
    ])
    let user = 7
    let refunded = false
    let collections = 0
    let logins = 0
    const errors = []
    const requests = []
    const metadata = () => ({
      order_id: 'synthetic-order',
      product_title: 'Purchased private guide',
      variant_name: 'Custom guide',
      quantity: 1,
      delivery_template: 'fixed-content',
      status: refunded ? 'refunded' : 'paid',
      pickup_login_required: true,
      pickup_code_required: true,
      pickup_login_satisfied: user === 9,
    })
    await context.route('**/*', async (route) => {
      const req = route.request()
      const url = new URL(req.url())
      if (url.origin !== base.origin) return route.abort('blockedbyclient')
      if (!url.pathname.startsWith('/api/')) return route.continue()
      requests.push(`${req.method()} ${url.pathname}`)
      let data
      let status = 200
      let failure
      if (url.pathname === '/api/status') {
        data = {
          system_name: 'Store test',
          self_use_mode_enabled: false,
          demo_site_enabled: false,
          display_in_currency: false,
          password_login_enabled: true,
          announcements_enabled: false,
          user_agreement_enabled: false,
          privacy_policy_enabled: false,
        }
      } else if (url.pathname === '/api/setup') {
        data = { status: true, root_init: true }
      } else if (url.pathname === '/api/user/auth/refresh') data = bundle(user)
      else if (url.pathname === '/api/user/self') data = bundle(user).user
      else if (url.pathname === '/api/notice') data = ''
      else if (url.pathname === '/api/user/login') {
        assert.equal(req.method(), 'POST')
        assert.equal(req.postDataJSON().username, 'synthetic-buyer')
        user = 9
        logins++
        data = bundle(user)
      } else if (url.pathname === `/api/user/auth/store-claim/${token}`) {
        if (req.method() === 'GET') data = metadata()
        else {
          assert.equal(req.method(), 'POST')
          collections++
          assert.equal(user, 9)
          assert.equal(refunded, false)
          if (req.postDataJSON().pickup_code !== 'correct-code') {
            status = 403
            failure = {
              success: false,
              code: 'STORE_PICKUP_CODE_INVALID',
              message:
                'Pickup code is incorrect. Check the original pickup code and try again.',
            }
          } else {
            data = {
              ...metadata(),
              product_id: 'synthetic-product',
              product_description: '',
              product_links: [],
              items: [],
              fixed_content:
                '# Verified private delivery\n\nSynthetic delivery only.',
            }
          }
        }
      } else if (url.pathname.includes('/refunds')) {
        data = {
          order_id: 'synthetic-order',
          product_title: 'Purchased private guide',
          variant_name: 'Custom guide',
          payment_method: 'platform:waffo_pancake',
          currency: 'USD',
          principal_quota: 500000,
          refunded_quota: refunded ? 500000 : 0,
          reserved_quota: 0,
          remaining_quota: refunded ? 0 : 500000,
          quantity: 1,
          refunded_quantity: refunded ? 1 : 0,
          max_quantity: refunded ? 0 : 1,
          eligible_items: [],
          supports_quantity: true,
          supports_amount: true,
          native_basis_verified: true,
          amount_minor: 100,
          refunded_amount_minor: refunded ? 100 : 0,
          remaining_amount_minor: refunded ? 0 : 100,
          supports_provider_sync: true,
          provider_reconciliation_pending: false,
          refunds: [],
        }
      } else if (req.method() === 'GET') data = []
      else {
        throw new Error(`Unexpected mutation: ${req.method()} ${url.pathname}`)
      }
      await route.fulfill({
        status,
        contentType: 'application/json',
        body: JSON.stringify(failure || { success: true, data }),
      })
    })
    const page = await context.newPage()
    page.on('pageerror', (error) => errors.push(error.message))
    try {
      await page.goto(new URL(pickup, base).href)
      const signIn = page.locator('a[href^="/sign-in?redirect="]').first()
      await signIn.waitFor()
      const target = new URL(await signIn.getAttribute('href'), base)
      assert.equal(target.pathname, '/sign-in')
      assert.equal(target.searchParams.get('redirect'), pickup)
      assert.equal(target.searchParams.get('reauth'), '1')
      assert.equal(collections, 0, 'wrong account cannot collect')
      await page.screenshot({
        path: path.join(output, `${width}-account-required.png`),
        fullPage: true,
      })
      await signIn.click()
      await page.getByLabel('Username or Email').fill('synthetic-buyer')
      await page
        .locator('input[name="password"]')
        .fill('synthetic-password-only')
      await page.getByRole('button', { name: 'Sign in', exact: true }).click()
      await page.waitForURL(new URL(pickup, base).href)
      await page.locator('#claim-code').fill('wrong-code')
      await page
        .getByRole('button', { name: 'Collect items', exact: true })
        .click()
      await page
        .getByText(
          'Pickup code is incorrect. Check the original pickup code and try again.'
        )
        .waitFor()
      assert.equal(await page.getByText('Verified private delivery').count(), 0)
      await page.screenshot({
        path: path.join(output, `${width}-code-error.png`),
        fullPage: true,
      })
      await page.locator('#claim-code').fill('correct-code')
      await page
        .getByRole('button', { name: 'Collect items', exact: true })
        .click()
      await page
        .getByRole('heading', { name: 'Verified private delivery' })
        .waitFor()
      assert.equal(logins, 1)
      assert.equal(collections, 2)
      assert.equal(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth
        ),
        true,
        'no horizontal overflow'
      )
      await page.screenshot({
        path: path.join(output, `${width}-collected.png`),
        fullPage: true,
      })
      refunded = true
      await page.reload()
      await page.getByText('This order has been refunded.').waitFor()
      assert.equal(
        await page
          .getByRole('button', { name: 'Collect items', exact: true })
          .count(),
        0
      )
      assert.equal(await page.getByText('Verified private delivery').count(), 0)
      assert.equal(collections, 2, 'reopening a refunded order never collects')
      await page.screenshot({
        path: path.join(output, `${width}-refunded.png`),
        fullPage: true,
      })
      assert.deepEqual(errors, [])
      results.push({
        width,
        height,
        colorScheme,
        logins,
        collections,
        errors,
        requests,
        result: 'pass',
      })
    } catch (error) {
      await page
        .screenshot({
          path: path.join(output, `${width}-failure.png`),
          fullPage: true,
        })
        .catch(() => {})
      results.push({
        width,
        errors,
        requests,
        result: 'fail',
        message: String(error),
      })
      throw error
    } finally {
      await writeFile(
        path.join(output, 'results.json'),
        JSON.stringify(results, null, 2)
      )
      await context.close()
    }
  }
} finally {
  await browser.close()
}
