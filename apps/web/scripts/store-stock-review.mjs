/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
// Run against an ALREADY RUNNING local web preview:
//   node apps/web/scripts/store-stock-review.mjs
// Use a production build and local preview, not the lazy development compiler.
// Optional: STOCK_REVIEW_URL=http://127.0.0.1:4189 STOCK_REVIEW_HEADED=1
// PLAYWRIGHT_MODULE and STOCK_REVIEW_CHROMIUM may select installed tooling.
// This script never starts a server or uses a personal browser/profile. Static
// web assets alone may reach the selected loopback origin. Every API response
// is synthetic; only auth bootstrap and explicitly armed quota writes are allowed.
import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const require = createRequire(import.meta.url)
let base
try {
  base = new URL(process.env.STOCK_REVIEW_URL || 'http://127.0.0.1:4189')
} catch {
  throw new Error('STOCK_REVIEW_URL must be a valid loopback HTTP(S) URL')
}
const loopback = (url) =>
  ['127.0.0.1', 'localhost', '[::1]'].includes(url.hostname)
assert.ok(loopback(base), 'STOCK_REVIEW_URL must be loopback')
assert.ok(
  ['http:', 'https:'].includes(base.protocol),
  'HTTP(S) preview required'
)
assert.equal(base.username + base.password, '', 'URL credentials are forbidden')
assert.equal(
  base.pathname,
  '/',
  'STOCK_REVIEW_URL must name the preview origin'
)
assert.equal(base.search + base.hash, '', 'URL query/fragment is forbidden')
const output = path.resolve(
  process.env.STOCK_REVIEW_OUTPUT || 'output/playwright/store-stock'
)
const headed = process.env.STOCK_REVIEW_HEADED === '1'
const QUOTA_HELP =
  'Unlimited sales removes the sales quota, not the inventory requirement. Import stock for inventory-based variants, or use fixed content for unlimited supply.'
const now = Math.floor(Date.now() / 1000)
const seller = {
  id: 9,
  username: 'stock-fixture-seller',
  display_name: 'Stock fixture seller',
}
const auth = {
  access_token: 'synthetic-stock-review-access',
  token_type: 'Bearer',
  access_expires_at: now + 3600,
  user: {
    id: seller.id,
    role: 1,
    username: seller.username,
    display_name: seller.display_name,
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
    sid: 'synthetic-stock-review-session',
    current: true,
    login_method: 'fixture',
    ip: '127.0.0.1',
    user_agent: 'fixture',
    created_at: now,
    last_active_at: now,
    expires_at: now + 7200,
  },
}
const config = {
  // Capability set for the floor-7 fixture, not a production schema claim.
  schema_floor: 7,
  fee_bps: 0,
  promotion_quota: 0,
  minimum_unit_price_quota: 500000,
  recipient_id: seller.id,
  linuxdo_units_per_usd: '1',
  disclaimer_version: 'stock-fixture-v1',
  disclaimer_text: 'Synthetic local terms.',
  platform_payment_methods: [],
  product_link_presets: [],
  store_catalogue_supported: true,
  store_access_supported: true,
  fixed_content_supported: true,
  product_variants_create_supported: true,
  product_purchase_limits_supported: true,
  store_categories_supported: true,
  store_collections_supported: false,
  store_likes_supported: false,
  store_merchant_home_supported: false,
  product_test_mode_supported: true,
}

// These are StoreProduct/StoreVariant projections (types.ts). No card keys,
// delivery content, pickup receipts, orders, or payment sessions are fabricated.
function productFixture({
  id,
  title,
  template,
  unlimitedSupply,
  stock = 0,
  quota = null,
  paused = false,
  price = 500000,
  tag = 'out_of_stock',
  paid = 0,
}) {
  const available = paused
    ? 0
    : unlimitedSupply
      ? (quota ?? 0)
      : Math.min(stock, quota ?? stock)
  const variantId = `${id}-variant`
  return {
    id,
    seller_id: seller.id,
    seller: { ...seller },
    title,
    description:
      'Synthetic stock display regression fixture. No deliverable content.',
    image_urls: [],
    contact: '',
    links: [],
    category_id: '',
    default_variant_id: variantId,
    price_quota: price,
    price_min_quota: price,
    price_max_quota: price,
    template,
    delivery_strategy: 'sequential',
    payment_methods: ['balance'],
    pickup_login_required: false,
    pickup_code_required: false,
    email_pickup_link: false,
    visibility: 'public',
    purchase_login_required: false,
    status: 'published',
    official: false,
    inventory_total: stock,
    inventory_available: stock,
    available_stock: stock,
    unlimited_supply: unlimitedSupply,
    sale_limit: quota === null ? null : paid + quota,
    paid_quantity: paid,
    reserved_quantity: 0,
    sale_available: available,
    max_quantity_per_order: null,
    max_quantity_per_buyer: null,
    buyer_purchase_remaining: null,
    promotion_expires_at: 0,
    created_at: now,
    updated_at: now,
    review_note: '',
    trading_paused: paused,
    catalogue: { custom_tags: [], auto_delivery: true, ai_processing: false },
    // Intentionally old server tags: the UI must reconcile these without
    // changing checkout's authoritative tradability checks.
    display_tags: [tag, 'auto_delivery'],
    net_paid_quantity: paid,
    likes: { supported: false, count: null, liked: false },
    variants: [
      {
        id: variantId,
        product_id: id,
        name: 'Fixture variant',
        price_quota: price,
        template,
        unlimited_supply: unlimitedSupply,
        enabled: true,
        is_default: true,
        created_at: now,
        updated_at: now,
        inventory_total: stock,
        inventory_available: stock,
        reserved_stock: 0,
        sale_available: available,
        trading_paused: paused,
      },
    ],
  }
}
const catalogueProducts = [
  productFixture({
    id: 'stock-fixed-unlimited',
    title: 'Fixture fixed content unlimited',
    template: 'fixed-content',
    unlimitedSupply: true,
  }),
  productFixture({
    id: 'stock-fixed-finite',
    title: 'Fixture fixed content quota five',
    template: 'fixed-content',
    unlimitedSupply: true,
    quota: 5,
    tag: 'in_stock',
  }),
  productFixture({
    id: 'stock-card-paused',
    title: 'Fixture nine cards trading paused',
    template: 'card-key',
    unlimitedSupply: false,
    stock: 9,
    paused: true,
    price: 74466,
  }),
  productFixture({
    id: 'stock-card-empty',
    title: 'Fixture empty cards unlimited sales',
    template: 'card-key',
    unlimitedSupply: false,
  }),
]
const merchantProduct = productFixture({
  id: 'stock-merchant-quota',
  title: 'Fixture merchant quota with nine cards',
  template: 'card-key',
  unlimitedSupply: false,
  stock: 9,
  paid: 2,
  tag: 'in_stock',
})
const pageOf = (items, limit = 24) => ({
  items,
  offset: 0,
  limit,
  has_more: false,
})
const terms = {
  seller_id: seller.id,
  version: 'stock-fixture-terms-v1',
  configured: true,
  content: 'Synthetic local merchant terms.',
  updated_at: now,
}
const inventorySnapshot = (product) => ({
  inventory_total: product.inventory_total,
  inventory_available: product.inventory_available,
  available_stock: product.available_stock,
  variants: product.variants.map((variant) => ({
    id: variant.id,
    inventory_total: variant.inventory_total,
    inventory_available: variant.inventory_available,
    reserved_stock: variant.reserved_stock,
  })),
})
const quotaSnapshot = (product) => ({
  id: product.id,
  sale_limit: product.sale_limit,
  sale_available: product.sale_available,
  paid_quantity: product.paid_quantity,
  reserved_quantity: product.reserved_quantity,
  unlimited_supply: product.unlimited_supply,
  trading_paused: product.trading_paused,
  inventory: inventorySnapshot(product),
})
function safeMessage(error) {
  return String(error?.message ?? error)
    .replaceAll(auth.access_token, '[redacted]')
    .replaceAll(auth.session.sid, '[redacted]')
    .replaceAll(/Bearer\s+\S+/gi, 'Bearer [redacted]')
    .replaceAll(
      /eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+/g,
      '[redacted JWT]'
    )
    .replaceAll(
      /((?:access_token|refresh_token|token|secret|password|cookie|authorization|sid)\s*[=:]\s*)\S+/gi,
      '$1[redacted]'
    )
    .replaceAll(/https?:\/\/[^\s"'<>]+/g, (value) => {
      try {
        const url = new URL(value)
        return `${url.origin}${url.pathname}`
      } catch {
        return '[URL omitted]'
      }
    })
    .slice(0, 1500)
}
const summary = {
  baseUrl: base.origin,
  output,
  headed,
  schemaFloor: 7,
  startedAt: new Date().toISOString(),
  status: 'running',
  safety: {
    isolatedBrowser: true,
    personalProfileUsed: false,
    apiPassThroughAllowed: false,
    ordersAndPaymentsAllowed: false,
  },
  viewports: [],
  errors: [],
}
let browser

function productContainer(page, product) {
  // Never select a product by position: multiple merchant products deliberately
  // coexist so an unscoped Unlimited sales switch cannot satisfy this review.
  assert.match(product.id, /^[a-zA-Z0-9-]+$/, 'Known fixture product ID')
  return page.locator(`article[data-store-product-id="${product.id}"]`)
}
function quotaFieldset(page, product) {
  // Current seller DOM uses an article containing a quota form, not a literal
  // <fieldset>. Bind the controls to the exact product.title and quota heading.
  return productContainer(page, product)
    .locator('form')
    .filter({
      has: page.getByRole('heading', {
        name: 'Available sales quota',
        exact: true,
      }),
    })
}

async function reviewViewport(viewport) {
  const result = {
    ...viewport,
    status: 'running',
    assertions: [],
    cases: [],
    requests: [],
    pages: [],
    errors: [],
  }
  summary.viewports.push(result)
  const state = {
    products: structuredClone(catalogueProducts),
    merchant: structuredClone(merchantProduct),
    armedWrite: null,
    quotaWrites: [],
    routeErrors: [],
  }
  const originalInventory = inventorySnapshot(state.merchant)
  const context = await browser.newContext({
    viewport: { width: viewport.width, height: viewport.height },
    locale: 'en-US',
    colorScheme: 'light',
    reducedMotion: 'reduce',
    serviceWorkers: 'block',
    acceptDownloads: false,
  })
  await context.addCookies([
    { name: 'vite-ui-theme', value: 'light', url: base.origin },
  ])
  await context.addInitScript(() => {
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('lmm.store.catalogue-view.v1', 'list')
  })
  // WebSockets bypass HTTP routing. Close them instead of connecting to an
  // API/backend (this preview does not need Vite HMR or live service events).
  if (context.routeWebSocket) {
    await context.routeWebSocket('**/*', (socket) => socket.close())
  }
  await context.route('**/*', async (route) => {
    const request = route.request()
    const method = request.method()
    const record = { method, path: '[unparsed]', disposition: 'pending' }
    result.requests.push(record)
    try {
      const url = new URL(request.url())
      record.path = url.pathname
      // The shared HTML includes this optional tracker. Supply an empty local
      // script, not a failed transport that fabricates a console error. All
      // other external traffic remains a hard failure; none reaches the network.
      if (
        method === 'GET' &&
        request.resourceType() === 'script' &&
        url.href === 'https://cdn.agentlane.com/v1/snippet.js'
      ) {
        record.disposition = 'optional-telemetry-fixture'
        await route.fulfill({
          status: 200,
          contentType: 'application/javascript',
          body: '/* Optional telemetry is disabled in this isolated review. */',
        })
        return
      }
      assert.ok(
        loopback(url) && url.origin === base.origin,
        `Unexpected external request ${method} ${url.origin}${url.pathname}`
      )
      if (!/^\/api(?:\/|$)/.test(url.pathname)) {
        // Rsbuild's loopback-only lazy compiler is development infrastructure,
        // not a backend mutation. No other non-API write is permitted.
        if (method === 'POST' && url.pathname === '/_rspack/lazy/trigger') {
          record.disposition = 'local-dev-compiler'
          await route.continue()
          return
        }
        assert.ok(
          ['GET', 'HEAD'].includes(method),
          `Unknown static write ${method} ${url.pathname}`
        )
        record.disposition = 'local-static'
        await route.continue()
        return
      }
      const pathname = url.pathname
      const isQuotaWrite =
        method === 'PUT' &&
        pathname ===
          `/api/store/products/${state.merchant.id}/sales-availability`
      const isRefresh =
        method === 'POST' && pathname === '/api/user/auth/refresh'
      // Payment SETTINGS below are read-only fixtures. Orders, payment actions,
      // inventory details/imports, and private fixed content are never allowed.
      assert.ok(
        !/\/orders(?:\/|$)|\/pay(?:\/|$)|\/claim(?:\/|$)|\/inventory(?:\/|$)|\/fixed-content(?:\/|$)/.test(
          pathname
        ),
        `Forbidden order/payment/inventory/content request ${method} ${pathname}`
      )
      assert.ok(
        method === 'GET' || isRefresh || isQuotaWrite,
        `Unexpected write ${method} ${pathname}`
      )
      let data
      if (isQuotaWrite) {
        assert.ok(
          state.armedWrite,
          'Quota mutation was not explicitly armed by the review'
        )
        const body = request.postDataJSON()
        assert.deepEqual(
          body,
          { available_count: state.armedWrite.available_count },
          'Exact quota request body'
        )
        record.body = { available_count: body.available_count }
        assert.ok(
          body.available_count === null ||
            (Number.isSafeInteger(body.available_count) &&
              body.available_count >= 0)
        )
        state.armedWrite = null
        state.merchant.sale_limit =
          body.available_count === null
            ? null
            : state.merchant.paid_quantity + body.available_count
        state.merchant.sale_available = Math.min(
          state.merchant.inventory_available,
          body.available_count ?? state.merchant.inventory_available
        )
        state.merchant.variants[0].sale_available =
          state.merchant.sale_available
        state.merchant.trading_paused = body.available_count === 0
        state.merchant.variants[0].trading_paused =
          state.merchant.trading_paused
        state.merchant.updated_at++
        assert.deepEqual(
          inventorySnapshot(state.merchant),
          originalInventory,
          'Quota fixture must not mutate physical inventory'
        )
        state.quotaWrites.push(structuredClone(record.body))
        record.quota = quotaSnapshot(state.merchant)
        data = null
      } else if (isRefresh) data = auth
      else if (pathname === '/api/status') {
        data = {
          system_name: 'Local stock regression fixture',
          self_use_mode_enabled: false,
          demo_site_enabled: false,
          display_in_currency: false,
          announcements_enabled: false,
          merchant_store_schema_floor: 7,
        }
      } else if (pathname === '/api/setup') {
        data = { status: true, root_init: true }
      } else if (pathname === '/api/user/self') data = auth.user
      else if (pathname === '/api/notice') data = ''
      else if (pathname === '/api/ratio-notifications') data = []
      else if (pathname === '/api/store/config') data = config
      else if (pathname === '/api/store/categories') {
        data = { supported: true, ...pageOf([], 100) }
      } else if (pathname === '/api/store/announcement') data = { content: '' }
      else if (pathname === '/api/store/disclaimer') {
        data = {
          version: 'stock-fixture-v1',
          text: config.disclaimer_text,
          accepted: true,
        }
      } else if (
        pathname === '/api/store/my/terms' ||
        /^\/api\/store\/products\/[a-zA-Z0-9-]+\/terms$/.test(pathname)
      ) {
        data = terms
      } else if (pathname === '/api/store/payments/settings') {
        data = {
          items: [],
          fee_bps: 0,
          categories: { platform_enabled: true, external_enabled: true },
        }
      } else if (
        pathname === '/api/store/cart' ||
        pathname === '/api/store/favorites'
      ) {
        data = pageOf([], 100)
      } else if (
        /^\/api\/store\/products\/[a-zA-Z0-9-]+\/likes$/.test(pathname)
      ) {
        data = { supported: false, count: null, liked: false }
      } else if (pathname === '/api/store/products') {
        // Reproduce the server's tradability filter, NOT physical-stock filtering.
        const stockFilter = url.searchParams.get('stock')
        assert.ok(
          !stockFilter || ['in_stock', 'out_of_stock'].includes(stockFilter),
          'Known stock filter only'
        )
        const items = state.products.filter((product) => {
          const tradable =
            !product.trading_paused &&
            (product.sale_available > 0 ||
              (product.unlimited_supply && product.sale_limit === null))
          return (
            !stockFilter || (stockFilter === 'in_stock' ? tradable : !tradable)
          )
        })
        data = pageOf(items)
      } else if (pathname === '/api/store/my/products') {
        data = pageOf([...state.products, state.merchant], 20)
        record.quota = quotaSnapshot(state.merchant)
      } else if (/^\/api\/store\/products\/[a-zA-Z0-9-]+$/.test(pathname)) {
        data = [...state.products, state.merchant].find(
          (product) => pathname === `/api/store/products/${product.id}`
        )
        assert.ok(data, `No fixture product for ${pathname}`)
      } else {
        throw new Error(
          `Missing API fixture ${method} ${pathname}; extend the fixture explicitly, never use the backend`
        )
      }
      record.disposition = 'fixture'
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ success: true, data }),
      })
    } catch (error) {
      record.disposition = 'rejected'
      const message = safeMessage(error)
      state.routeErrors.push(message)
      result.errors.push({ source: 'route', message })
      // Throw above, capture here, abort the transport, and rethrow from the
      // acceptance check. A rejected request can never become a silent pass.
      await route.abort('blockedbyclient').catch(() => undefined)
    }
  })

  async function trackedPage(name) {
    const page = await context.newPage()
    page.setDefaultTimeout(15000)
    page.setDefaultNavigationTimeout(45000)
    const log = { name, pageErrors: [], consoleErrors: [] }
    result.pages.push(log)
    page.on('pageerror', (error) =>
      log.pageErrors.push({ name: error.name, message: safeMessage(error) })
    )
    page.on('console', (message) => {
      if (message.type() === 'error') {
        // Console messages can contain auth headers or application secrets.
        log.consoleErrors.push({
          type: 'error',
          message: safeMessage(message.text()),
          location: {
            url: safeMessage(message.location().url),
            lineNumber: message.location().lineNumber,
            columnNumber: message.location().columnNumber,
          },
        })
      }
    })
    return { page, log }
  }
  async function checked(testCase, name, action) {
    const assertion = { case: testCase.name, name, status: 'running' }
    result.assertions.push(assertion)
    testCase.assertions.push(assertion)
    try {
      await action()
      assertion.status = 'passed'
    } catch (error) {
      assertion.status = 'failed'
      assertion.error = safeMessage(error)
      throw error
    }
  }
  async function runCase(name, tracked, action) {
    const testCase = {
      name,
      status: 'running',
      assertions: [],
      errors: [],
      screenshot: `${name}-${viewport.width}.png`,
    }
    result.cases.push(testCase)
    try {
      await action(testCase)
      await checked(
        testCase,
        'No horizontal overflow at this state',
        async () => {
          const dimensions = await tracked.page.evaluate(() => ({
            viewport: window.innerWidth,
            document: document.documentElement.scrollWidth,
            body: document.body.scrollWidth,
          }))
          assert.ok(
            dimensions.document <= dimensions.viewport + 1 &&
              dimensions.body <= dimensions.viewport + 1,
            JSON.stringify(dimensions)
          )
        }
      )
      await checked(
        testCase,
        'Every observed API request was fixture-routed; no forbidden write',
        async () => assert.deepEqual(state.routeErrors, [])
      )
      await checked(testCase, 'No pageerror or console.error', async () => {
        assert.deepEqual(tracked.log.pageErrors, [])
        assert.deepEqual(tracked.log.consoleErrors, [])
      })
      testCase.status = 'passed'
    } catch (error) {
      testCase.status = 'failed'
      const message = safeMessage(error)
      testCase.errors.push(message)
      result.errors.push({ source: name, message })
    } finally {
      try {
        await tracked.page.screenshot({
          path: path.join(output, testCase.screenshot),
          fullPage: true,
          animations: 'disabled',
        })
      } catch (error) {
        testCase.status = 'failed'
        testCase.errors.push(`Screenshot failed: ${safeMessage(error)}`)
        result.errors.push({
          source: name,
          message: 'Screenshot capture failed',
        })
      }
    }
  }
  async function goto(page, pathname) {
    await page.goto(new URL(pathname, base).href, {
      waitUntil: 'domcontentloaded',
    })
  }
  async function verifyTag(testCase, page, product, expected, forbidden) {
    const container = productContainer(page, product)
    await checked(
      testCase,
      `Exact product title identifies one rendered article: ${product.title}`,
      async () => {
        await container
          .getByRole('heading', { name: product.title, exact: true })
          .waitFor()
        assert.equal(await container.count(), 1)
      }
    )
    await checked(
      testCase,
      `Visible ${expected}; absent ${forbidden.join(', ')}`,
      async () => {
        const tags = container.locator('[aria-label="Product tags"]')
        await tags.getByText(expected, { exact: true }).waitFor()
        for (const label of forbidden) {
          assert.equal(await tags.getByText(label, { exact: true }).count(), 0)
        }
      }
    )
  }
  async function detailCase(name, product, expectedEnabled) {
    const tracked = await trackedPage(name)
    await runCase(name, tracked, async (testCase) => {
      await checked(testCase, 'Render the fixture product detail', async () => {
        await goto(tracked.page, `/store/products/${product.id}`)
        await tracked.page
          .getByRole('heading', { name: product.title, exact: true })
          .waitFor()
      })
      await checked(
        testCase,
        'Synthetic authentication and accepted disclaimer avoid unrelated checkout gates',
        async () => {
          await tracked.page
            .getByRole('radio', { name: 'Balance payment', exact: true })
            .waitFor()
          assert.ok(
            result.requests.some(
              (request) =>
                request.path === '/api/user/auth/refresh' &&
                request.disposition === 'fixture'
            )
          )
          assert.ok(
            result.requests.some(
              (request) =>
                request.path === '/api/store/disclaimer' &&
                request.disposition === 'fixture'
            )
          )
        }
      )
      await checked(
        testCase,
        expectedEnabled
          ? 'Positive control: stocked fixed-content checkout is enabled (never clicked)'
          : 'Unavailable checkout cannot create an order',
        async () => {
          const button = tracked.page.getByRole('button', {
            name: 'Place order',
            exact: true,
          })
          await button.waitFor()
          // Terms/config are async; wait for their real UI state, not a sleep.
          await tracked.page.waitForFunction((enabled) => {
            const button = [...document.querySelectorAll('button')].find(
              (node) => node.textContent.trim() === 'Place order'
            )
            return button && button.disabled === !enabled
          }, expectedEnabled)
          assert.equal(await button.isDisabled(), !expectedEnabled)
          assert.equal(
            result.requests.filter((request) =>
              /\/orders(?:\/|$)|\/pay(?:\/|$)/.test(request.path)
            ).length,
            0
          )
        }
      )
      if (product.trading_paused) {
        await checked(
          testCase,
          'Paused detail shows Trading paused, not Out of stock; purchase remains disabled',
          async () => {
            const tags = tracked.page.locator('[aria-label="Product tags"]')
            await tags.getByText('Trading paused', { exact: true }).waitFor()
            assert.equal(
              await tags.getByText('Out of stock', { exact: true }).count(),
              0
            )
            await tracked.page
              .getByText(
                'This product or payment method is currently unavailable.',
                { exact: true }
              )
              .waitFor()
          }
        )
      }
    })
    await tracked.page.close()
  }

  try {
    const list = await trackedPage('catalogue-list')
    await goto(list.page, '/store')
    const cases = [
      [
        'case1-fixed-unlimited-list',
        'In stock',
        ['Out of stock', 'Trading paused'],
      ],
      [
        'case2-fixed-finite-list',
        'In stock',
        ['Out of stock', 'Trading paused'],
      ],
      ['case3-nine-cards-paused-list', 'Trading paused', ['Out of stock']],
      ['case4-empty-cards-unlimited-sales-list', 'Out of stock', ['In stock']],
    ]
    for (const [index, [name, expected, forbidden]] of cases.entries()) {
      await runCase(name, list, async (testCase) => {
        await checked(
          testCase,
          'Fixture has the declared inventory/quota/tradability state',
          async () => {
            const product = state.products[index]
            if (index === 0) {
              assert.equal(product.unlimited_supply, true)
              assert.equal(product.sale_limit, null)
              assert.equal(product.sale_available, 0)
              assert.ok(product.display_tags.includes('out_of_stock'))
            } else if (index === 1) {
              assert.equal(product.sale_limit, 5)
              assert.equal(product.sale_available, 5)
              assert.equal(product.unlimited_supply, true)
            } else if (index === 2) {
              assert.equal(product.inventory_total, 9)
              assert.equal(product.inventory_available, 9)
              assert.equal(product.trading_paused, true)
              assert.equal(product.price_quota, 74466)
              assert.ok(product.price_quota < config.minimum_unit_price_quota)
            } else {
              assert.equal(product.sale_limit, null)
              assert.equal(product.unlimited_supply, false)
              assert.equal(product.inventory_total, 0)
              assert.equal(product.sale_available, 0)
            }
          }
        )
        await verifyTag(
          testCase,
          list.page,
          state.products[index],
          expected,
          forbidden
        )
      })
    }
    await list.page.close()
    await detailCase(
      'case1-fixed-unlimited-detail-positive-control',
      state.products[0],
      true
    )
    await detailCase('case3-nine-cards-paused-detail', state.products[2], false)
    await detailCase(
      'case4-empty-cards-unlimited-sales-detail',
      state.products[3],
      false
    )

    const merchant = await trackedPage('merchant-quota')
    await goto(merchant.page, '/store/manage')
    await runCase(
      'case5-merchant-unlimited-initial',
      merchant,
      async (testCase) => {
        await checked(
          testCase,
          'Product title scopes exactly one sales-quota control group, despite other products',
          async () => {
            const fieldset = quotaFieldset(merchant.page, merchantProduct)
            await fieldset
              .getByRole('switch', { name: 'Unlimited sales', exact: true })
              .waitFor()
            assert.equal(await fieldset.count(), 1)
            assert.equal(
              await fieldset
                .getByRole('switch', { name: 'Unlimited sales', exact: true })
                .getAttribute('aria-checked'),
              'true'
            )
          }
        )
        await checked(
          testCase,
          'Unlimited-sales inventory requirement helper is visible',
          async () => {
            await quotaFieldset(merchant.page, merchantProduct)
              .getByText(QUOTA_HELP, { exact: true })
              .waitFor()
          }
        )
      }
    )
    for (const [index, amount] of [5, 0, null].entries()) {
      const name = `case5-merchant-save-${amount === null ? 'unlimited' : amount}`
      await runCase(name, merchant, async (testCase) => {
        const fieldset = quotaFieldset(merchant.page, merchantProduct)
        const toggle = fieldset.getByRole('switch', {
          name: 'Unlimited sales',
          exact: true,
        })
        await checked(
          testCase,
          `Set remaining sales quota to ${amount === null ? 'unlimited' : amount}`,
          async () => {
            await toggle.waitFor()
            const checked =
              (await toggle.getAttribute('aria-checked')) === 'true'
            if (checked !== (amount === null)) await toggle.click()
            if (amount !== null) {
              await fieldset
                .getByLabel('Remaining sales quota', { exact: true })
                .fill(String(amount))
            }
          }
        )
        let reloadedProduct
        await checked(
          testCase,
          `Save sends exactly PUT sales-availability {available_count:${amount}} and triggers GET`,
          async () => {
            state.armedWrite = { available_count: amount }
            const [putResponse, getResponse] = await Promise.all([
              merchant.page.waitForResponse(
                (response) =>
                  new URL(response.url()).pathname ===
                    `/api/store/products/${merchantProduct.id}/sales-availability` &&
                  response.request().method() === 'PUT'
              ),
              merchant.page.waitForResponse(
                (response) =>
                  new URL(response.url()).pathname ===
                    '/api/store/my/products' &&
                  response.request().method() === 'GET'
              ),
              fieldset
                .getByRole('button', { name: 'Save sales quota', exact: true })
                .click(),
            ])
            assert.equal(putResponse.status(), 200)
            assert.deepEqual(putResponse.request().postDataJSON(), {
              available_count: amount,
            })
            const body = await getResponse.json()
            assert.equal(body.success, true)
            reloadedProduct = body.data.items.find(
              (product) => product.id === merchantProduct.id
            )
            assert.ok(reloadedProduct, 'Reloaded GET includes target product')
            assert.deepEqual(
              state.quotaWrites,
              [5, 0, null]
                .slice(0, index + 1)
                .map((available_count) => ({ available_count }))
            )
            assert.equal(state.armedWrite, null)
          }
        )
        await checked(
          testCase,
          'Re-fetched projection preserves physical stock and correctly reflects the remaining quota',
          async () => {
            assert.deepEqual(
              inventorySnapshot(reloadedProduct),
              originalInventory
            )
            assert.equal(
              reloadedProduct.sale_limit,
              amount === null ? null : merchantProduct.paid_quantity + amount
            )
            assert.equal(
              reloadedProduct.sale_available,
              Math.min(9, amount ?? 9)
            )
            assert.equal(reloadedProduct.trading_paused, amount === 0)
            assert.equal(
              reloadedProduct.variants[0].trading_paused,
              amount === 0
            )
            assert.equal(
              reloadedProduct.unlimited_supply,
              false,
              'Unlimited sales does not convert card inventory into unlimited supply'
            )
          }
        )
        await checked(
          testCase,
          'A fresh page GET, not just optimistic local state, restores the saved controls',
          async () => {
            const [response] = await Promise.all([
              merchant.page.waitForResponse(
                (response) =>
                  new URL(response.url()).pathname ===
                    '/api/store/my/products' &&
                  response.request().method() === 'GET'
              ),
              merchant.page.reload({ waitUntil: 'domcontentloaded' }),
            ])
            const fresh = (await response.json()).data.items.find(
              (product) => product.id === merchantProduct.id
            )
            assert.deepEqual(
              quotaSnapshot(fresh),
              quotaSnapshot(reloadedProduct)
            )
            const freshFieldset = quotaFieldset(merchant.page, merchantProduct)
            const freshToggle = freshFieldset.getByRole('switch', {
              name: 'Unlimited sales',
              exact: true,
            })
            await freshToggle.waitFor()
            assert.equal(
              await freshToggle.getAttribute('aria-checked'),
              String(amount === null)
            )
            if (amount !== null) {
              assert.equal(
                await freshFieldset
                  .getByLabel('Remaining sales quota', { exact: true })
                  .inputValue(),
                String(amount)
              )
            } else {
              await freshFieldset
                .getByText(QUOTA_HELP, { exact: true })
                .waitFor()
            }
          }
        )
      })
      state.armedWrite = null
    }
    await runCase(
      'case5-merchant-final-invariants',
      merchant,
      async (testCase) => {
        await checked(
          testCase,
          'Exactly three quota writes: 5, 0, null; no inventory/order/payment writes',
          async () => {
            assert.deepEqual(state.quotaWrites, [
              { available_count: 5 },
              { available_count: 0 },
              { available_count: null },
            ])
            const mutations = result.requests.filter(
              (request) => !['GET', 'HEAD', 'OPTIONS'].includes(request.method)
            )
            assert.ok(
              mutations.every(
                (request) =>
                  (request.disposition === 'local-dev-compiler' &&
                    request.method === 'POST' &&
                    request.path === '/_rspack/lazy/trigger') ||
                  (request.disposition === 'fixture' &&
                    ((request.method === 'POST' &&
                      request.path === '/api/user/auth/refresh') ||
                      (request.method === 'PUT' &&
                        request.path ===
                          `/api/store/products/${merchantProduct.id}/sales-availability`)))
              )
            )
            assert.deepEqual(
              inventorySnapshot(state.merchant),
              originalInventory
            )
            assert.equal(state.merchant.sale_limit, null)
            assert.equal(state.merchant.sale_available, 9)
          }
        )
      }
    )
    await merchant.page.close()
  } catch (error) {
    result.errors.push({ source: 'viewport', message: safeMessage(error) })
  } finally {
    result.quotaWrites = state.quotaWrites
    result.initialInventory = originalInventory
    result.finalInventory = inventorySnapshot(state.merchant)
    await context.close()
    result.status =
      result.errors.length ||
      result.cases.some((testCase) => testCase.status !== 'passed')
        ? 'failed'
        : 'passed'
    // Late page/console/route errors must still fail the complete viewport.
    if (
      state.routeErrors.length ||
      result.pages.some(
        (page) => page.pageErrors.length || page.consoleErrors.length
      )
    ) {
      result.status = 'failed'
    }
  }
}

try {
  await mkdir(output, { recursive: true })
  let entry
  try {
    entry = process.env.PLAYWRIGHT_MODULE || require.resolve('playwright')
  } catch {
    entry = require.resolve('/usr/lib/node_modules/playwright')
  }
  const imported = await import(pathToFileURL(entry).href)
  const chromium = imported.chromium ?? imported.default?.chromium
  assert.ok(chromium, 'Playwright Chromium is required')
  browser = await chromium.launch({
    executablePath: process.env.STOCK_REVIEW_CHROMIUM || undefined,
    headless: !headed,
    args: [
      '--disable-background-networking',
      '--disable-component-update',
      '--disable-sync',
      '--disable-extensions',
      '--no-first-run',
    ],
  })
  for (const viewport of [
    { name: 'desktop', width: 1440, height: 900 },
    { name: 'mobile', width: 390, height: 844 },
  ]) {
    await reviewViewport(viewport)
  }
  summary.status =
    summary.viewports.length === 2 &&
    summary.viewports.every((viewport) => viewport.status === 'passed')
      ? 'passed'
      : 'failed'
} catch (error) {
  summary.status = 'failed'
  summary.errors.push({ source: 'runner', message: safeMessage(error) })
} finally {
  if (browser) {
    try {
      await browser.close()
    } catch (error) {
      summary.status = 'failed'
      summary.errors.push({
        source: 'browser-close',
        message: safeMessage(error),
      })
    }
  }
  summary.finishedAt = new Date().toISOString()
  await mkdir(output, { recursive: true })
  await writeFile(
    path.join(output, 'summary.json'),
    `${JSON.stringify(summary, null, 2)}\n`
  )
}
console.log(
  `Stock review ${summary.status}: ${path.join(output, 'summary.json')}`
)
if (summary.status !== 'passed') {
  process.exitCode = 1
  throw new Error(
    `Stock browser regression failed; inspect ${path.join(output, 'summary.json')}`
  )
}
