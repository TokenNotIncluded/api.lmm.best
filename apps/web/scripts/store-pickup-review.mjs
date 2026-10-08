/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// Local browser verification with synthetic responses. No request reaches a
// backend, payment provider, merchant website, or delivery email service.
import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const require = createRequire(import.meta.url)
const playwrightEntry =
  process.env.PLAYWRIGHT_MODULE ||
  (() => {
    try {
      return require.resolve('playwright')
    } catch {
      return require.resolve('playwright', { paths: ['/usr/lib/node_modules'] })
    }
  })()
const { chromium } = (await import(pathToFileURL(playwrightEntry).href)).default
const base = new URL(process.env.STORE_REVIEW_URL || 'http://127.0.0.1:4183')
assert.equal(base.protocol, 'http:')
assert.equal(base.hostname, '127.0.0.1', 'Review is restricted to loopback')
const output =
  process.env.STORE_REVIEW_OUTPUT ||
  path.resolve('output/playwright/store-pickup')
await mkdir(output, { recursive: true })
const now = Math.floor(Date.now() / 1000)
const variantName = '自定义规格 / 企业席位 17人 <商家原名>'
const items = ['FIRST-KEY', 'SECOND-KEY\nMerchant line two', 'THIRD-KEY']
const description = `## Merchant instructions

| Specification | Details |
| --- | --- |
| Arbitrary merchant choice | Custom description |

1. Read the guide
2. Use the supplied key

[Merchant guide](https://merchant.example.test/guide)

<script>window.storeMarkdownExecuted = true</script>
<img src="https://merchant.example.test/image.png" onerror="window.storeMarkdownExecuted = true">
<a href="javascript:window.storeMarkdownExecuted=true" onclick="window.storeMarkdownExecuted=true">Unsafe link</a>`
const product = {
  id: 'product-fixture',
  seller_id: 9,
  title: 'Merchant fixture product',
  description,
  image_urls: [],
  contact: 'Merchant support',
  links: [],
  price_quota: 500000,
  template: 'card-key',
  delivery_strategy: 'sequential',
  payment_methods: ['balance'],
  pickup_login_required: false,
  pickup_code_required: false,
  email_pickup_link: false,
  status: 'published',
  official: false,
  available_stock: 3,
  promotion_expires_at: 0,
  created_at: now,
  updated_at: now,
  review_note: '',
}
const metadata = {
  order_id: 'order-fixture',
  product_title: product.title,
  variant_name: variantName,
  quantity: items.length,
  status: 'paid',
  pickup_login_required: false,
  pickup_code_required: false,
  pickup_login_satisfied: true,
}
const auth = {
  access_token: 'synthetic-store-review',
  token_type: 'Bearer',
  access_expires_at: now + 3600,
  user: {
    id: 9,
    role: 1,
    username: 'synthetic-seller',
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
    sid: 'synthetic',
    current: true,
    login_method: 'fixture',
    ip: '127.0.0.1',
    user_agent: 'fixture',
    created_at: now,
    last_active_at: now,
    expires_at: now + 7200,
  },
}
const results = []
const browser = await chromium.launch({
  headless: true,
  ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH
    ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH }
    : {}),
})
try {
  for (const [width, height, colorScheme] of [
    [1440, 900, 'light'],
    [390, 844, 'dark'],
    [320, 740, 'light'],
  ]) {
    const context = await browser.newContext({
      viewport: { width, height },
      colorScheme,
      locale: 'en-US',
      reducedMotion: 'reduce',
      serviceWorkers: 'block',
    })
    const currentProduct = { ...product }
    let present = true
    let unlistAttempts = 0
    let deleteAttempts = 0
    const requests = []
    const pageErrors = []
    const consoleErrors = []
    await context.addCookies([
      { name: 'vite-ui-theme', value: colorScheme, url: base.origin },
    ])
    await context.addInitScript(() => {
      localStorage.setItem('i18nextLng', 'en')
      window.storeMarkdownExecuted = false
      window.storeCopied = []
      Object.defineProperty(navigator.clipboard, 'writeText', {
        configurable: true,
        value: async (value) => {
          window.storeCopied.push(value)
        },
      })
    })
    await context.route('**/*', async (route) => {
      const request = route.request()
      const url = new URL(request.url())
      if (url.origin !== base.origin) return route.abort('blockedbyclient')
      if (!url.pathname.startsWith('/api/')) return route.continue()
      requests.push({ method: request.method(), path: url.pathname })
      let data
      let failure
      if (url.pathname === '/api/status') {
        data = {
          system_name: 'LMM Forge',
          self_use_mode_enabled: false,
          demo_site_enabled: false,
          display_in_currency: false,
          announcements_enabled: false,
        }
      } else if (url.pathname === '/api/setup') {
        data = { status: true, root_init: true }
      } else if (url.pathname === '/api/user/auth/refresh') data = auth
      else if (url.pathname === '/api/user/self') data = auth.user
      else if (url.pathname === '/api/notice') data = ''
      else if (
        url.pathname === '/api/store/products/product-fixture' &&
        request.method() === 'GET'
      ) {
        data = currentProduct
      } else if (url.pathname === '/api/store/disclaimer') {
        data = { version: 'v1', text: 'Terms', accepted: false }
      } else if (url.pathname.startsWith('/api/user/auth/store-claim/')) {
        data =
          request.method() === 'GET'
            ? metadata
            : {
                ...metadata,
                product_id: product.id,
                product_description: description,
                product_links: [
                  {
                    title: 'Download guide',
                    url: 'https://merchant.example.test/download',
                    description: 'Merchant supplied link',
                  },
                  {
                    title: 'Unsafe product link',
                    url: 'javascript:alert(1)',
                    description: '',
                  },
                ],
                items,
              }
      } else if (url.pathname === '/api/store/my/products') {
        data = {
          items: present ? [currentProduct] : [],
          has_more: false,
          offset: 0,
          limit: 20,
        }
      } else if (url.pathname === '/api/store/config') {
        data = { fee_bps: 0, promotion_quota: 0, platform_payment_methods: [] }
      } else if (url.pathname === '/api/store/payments/settings') {
        data = { items: [], fee_bps: 0 }
      } else if (
        url.pathname === '/api/store/products/product-fixture/unlist'
      ) {
        assert.equal(request.method(), 'POST')
        unlistAttempts++
        if (unlistAttempts === 1) failure = 'Fixture unlisting failed'
        else {
          currentProduct.status = 'unlisted'
          data = null
        }
      } else if (
        url.pathname === '/api/store/products/product-fixture' &&
        request.method() === 'DELETE'
      ) {
        deleteAttempts++
        if (deleteAttempts === 1) failure = 'Fixture deletion failed'
        else {
          present = false
          data = null
        }
      } else if (request.method() === 'GET') data = []
      else {
        throw new Error(
          `Unexpected mutation ${request.method()} ${url.pathname}`
        )
      }
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(
          failure
            ? { success: false, message: failure }
            : { success: true, data }
        ),
      })
    })
    const page = await context.newPage()
    page.on('pageerror', (error) => pageErrors.push(error.message))
    page.on('console', (message) => {
      if (message.type() === 'error') consoleErrors.push(message.text())
    })
    async function verifyMarkdown() {
      await page
        .getByRole('heading', { name: 'Merchant instructions', exact: true })
        .waitFor({ timeout: 30000 })
        .catch(async (error) => {
          await page.screenshot({
            path: path.join(output, `failure-${width}.png`),
            fullPage: true,
          })
          console.error(
            JSON.stringify(
              {
                url: page.url(),
                body: await page.locator('body').innerText(),
                pageErrors,
                consoleErrors,
                requests,
              },
              null,
              2
            )
          )
          throw error
        })
      assert.equal(
        await page.locator('table th').first().innerText(),
        'Specification'
      )
      assert.equal(
        await page.locator('table td').first().innerText(),
        'Arbitrary merchant choice'
      )
      assert.equal(await page.locator('ol > li').count(), 2)
      const guide = page.getByRole('link', {
        name: 'Merchant guide',
        exact: true,
      })
      assert.equal(await guide.getAttribute('rel'), 'noopener noreferrer')
      assert.equal(await guide.getAttribute('target'), '_blank')
      assert.equal(
        await page
          .locator('[onerror], [onclick], a[href^="javascript:"]')
          .count(),
        0
      )
      assert.equal(
        await page.evaluate(() => window.storeMarkdownExecuted),
        false
      )
      assert.equal(
        await page.evaluate(() =>
          [...document.querySelectorAll('script')].some((node) =>
            node.textContent.includes('storeMarkdownExecuted = true')
          )
        ),
        false
      )
      assert.equal(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= window.innerWidth + 1
        ),
        true,
        'No page-wide horizontal overflow'
      )
      assert.equal(
        await page.evaluate(
          (theme) => document.documentElement.classList.contains(theme),
          colorScheme
        ),
        true,
        'Requested light or dark theme is active'
      )
    }
    await page.goto(new URL('/store/products/product-fixture', base).href, {
      waitUntil: 'domcontentloaded',
      timeout: 60000,
    })
    await verifyMarkdown()
    await page.screenshot({
      path: path.join(output, `product-${width}-${colorScheme}.png`),
      fullPage: true,
    })
    await page.goto(new URL(`/store/claim/${'c'.repeat(43)}`, base).href, {
      waitUntil: 'domcontentloaded',
      timeout: 60000,
    })
    await page
      .getByRole('button', { name: 'Collect items', exact: true })
      .waitFor()
    assert.equal(await page.locator('textarea').count(), 0)
    assert.equal(
      await page
        .getByRole('heading', { name: 'Merchant instructions', exact: true })
        .count(),
      0
    )
    await page
      .getByRole('button', { name: 'Collect items', exact: true })
      .click()
    await verifyMarkdown()
    assert.equal(
      await page
        .getByText(`Specification: ${variantName}`, { exact: true })
        .count(),
      4
    )
    assert.equal(
      await page
        .getByRole('button', { name: 'Copy selected items', exact: true })
        .isDisabled(),
      true
    )
    await page
      .getByRole('checkbox', { name: 'Select item 3', exact: true })
      .check()
    await page
      .getByRole('checkbox', { name: 'Select item 1', exact: true })
      .check()
    await page
      .getByRole('button', { name: 'Copy selected items', exact: true })
      .click()
    assert.equal(
      await page.evaluate(() => window.storeCopied.at(-1)),
      `${items[0]}\n${items[2]}`
    )
    await page
      .getByRole('button', { name: 'Invert selection', exact: true })
      .click()
    await page
      .getByRole('button', { name: 'Copy selected items', exact: true })
      .click()
    assert.equal(await page.evaluate(() => window.storeCopied.at(-1)), items[1])
    await page.getByRole('button', { name: 'Select all', exact: true }).click()
    await page
      .getByRole('button', { name: 'Copy selected items', exact: true })
      .click()
    assert.equal(
      await page.evaluate(() => window.storeCopied.at(-1)),
      items.join('\n')
    )
    await page.screenshot({
      path: path.join(output, `pickup-${width}-${colorScheme}.png`),
      fullPage: true,
    })
    await page.goto(new URL('/store/manage', base).href, {
      waitUntil: 'domcontentloaded',
      timeout: 60000,
    })
    await page
      .getByRole('button', { name: 'Unlist product', exact: true })
      .waitFor()
    await page
      .getByRole('button', { name: 'Unlist product', exact: true })
      .click()
    assert.equal(unlistAttempts, 0)
    await page
      .getByRole('alertdialog')
      .getByRole('button', { name: 'Cancel', exact: true })
      .click()
    await page.getByRole('alertdialog').waitFor({ state: 'hidden' })
    await page
      .getByRole('button', { name: 'Unlist product', exact: true })
      .click()
    await page
      .getByRole('alertdialog')
      .getByRole('button', { name: 'Unlist product', exact: true })
      .click()
    await page
      .getByRole('alertdialog')
      .getByText('Fixture unlisting failed', { exact: true })
      .waitFor()
    assert.equal(await page.locator('article').count(), 1)
    await page
      .getByRole('alertdialog')
      .getByRole('button', { name: 'Unlist product', exact: true })
      .click()
    await page.getByRole('alertdialog').waitFor({ state: 'hidden' })
    assert.equal(await page.locator('article').count(), 1)
    assert.equal(
      await page
        .getByRole('button', { name: 'Unlist product', exact: true })
        .count(),
      0
    )
    await page
      .getByRole('button', { name: 'Delete product', exact: true })
      .click()
    assert.equal(deleteAttempts, 0)
    await page
      .getByRole('alertdialog')
      .getByRole('button', { name: 'Delete product', exact: true })
      .click()
    await page
      .getByRole('alertdialog')
      .getByText('Fixture deletion failed', { exact: true })
      .waitFor()
    assert.equal(await page.locator('article').count(), 1)
    await page.screenshot({
      path: path.join(output, `delete-retry-${width}-${colorScheme}.png`),
      fullPage: true,
    })
    await page
      .getByRole('alertdialog')
      .getByRole('button', { name: 'Delete product', exact: true })
      .click()
    await page.getByRole('alertdialog').waitFor({ state: 'hidden' })
    assert.equal(await page.locator('article').count(), 0)
    assert.equal(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth + 1
      ),
      true
    )
    assert.deepEqual(pageErrors, [])
    results.push({
      width,
      height,
      colorScheme,
      markdown: true,
      sanitizer: true,
      selection: true,
      lifecycle: true,
      pageErrors,
      requests,
    })
    await context.close()
  }
} finally {
  await browser.close()
}
await writeFile(
  path.join(output, 'report.json'),
  `${JSON.stringify(results, null, 2)}\n`
)
console.log(
  `Store browser verification passed at ${results.length} viewports: ${output}`
)
