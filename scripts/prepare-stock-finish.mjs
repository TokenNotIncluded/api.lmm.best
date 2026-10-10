import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import path from 'node:path'
import { createHash } from 'node:crypto'
import { pathToFileURL } from 'node:url'

const SOURCE = 'b16db4fc2ad6083f507a82e5d637633a773f146c'
const BASE = '3e825da797e4577d1263940763a72c089ed0f3b6'
const out = process.env.STOCK_PREPARE_OUTPUT || '/tmp/stock-prepared'
mkdirSync(out, { recursive: true })
const changed = new Set()
const git = (...args) => execFileSync('git', args, { encoding: 'utf8', maxBuffer: 32 << 20 })
const source = (file, ref = SOURCE) => git('show', `${ref}:${file}`)
function save(file, text) { writeFileSync(file, text); changed.add(file) }
function once(text, old, next) {
  assert.equal(text.split(old).length, 2, `Expected one patch site: ${old.slice(0, 90)}`)
  return text.replace(old, next)
}

const reviewFile = 'apps/web/scripts/store-stock-review.mjs'
let review = source(reviewFile)
review = once(review,
  '// Optional: STOCK_REVIEW_URL=http://127.0.0.1:4189 STOCK_REVIEW_HEADED=1',
  '// Use a production build and local preview, not the lazy development compiler.\n// Optional: STOCK_REVIEW_URL=http://127.0.0.1:4189 STOCK_REVIEW_HEADED=1\n// PLAYWRIGHT_MODULE and STOCK_REVIEW_CHROMIUM may select installed tooling.')
review = once(review,
  '  return page.locator("article").filter({\n    has: page.getByRole("heading", { name: product.title, exact: true }),\n  });',
  '  assert.match(product.id, /^[a-zA-Z0-9-]+$/, "Known fixture product ID");\n  return page.locator(`article[data-store-product-id="${product.id}"]`);')
review = once(review,
  '      if (!loopback(url) || url.origin !== base.origin) {\n        record.disposition = "aborted-external";\n        if (!["GET", "HEAD", "OPTIONS"].includes(method))\n          {throw new Error(`Forbidden external write ${method}`);}\n        await route.abort("blockedbyclient");\n        return;\n      }',
  '      // The shared HTML includes this optional tracker. Supply an empty local\n      // script, not a failed transport that fabricates a console error. All\n      // other external traffic remains a hard failure; none reaches the network.\n      if (method === "GET" && request.resourceType() === "script" &&\n          url.href === "https://cdn.agentlane.com/v1/snippet.js") {\n        record.disposition = "optional-telemetry-fixture";\n        await route.fulfill({ status: 200, contentType: "application/javascript",\n          body: "/* Optional telemetry is disabled in this isolated review. */" });\n        return;\n      }\n      assert.ok(loopback(url) && url.origin === base.origin,\n        `Unexpected external request ${method} ${url.origin}${url.pathname}`);')
review = once(review,
  '          message:\n            "Console error captured; raw contents omitted to avoid secrets.",',
  '          message: safeMessage(message.text()),\n          location: {\n            url: safeMessage(message.location().url),\n            lineNumber: message.location().lineNumber,\n            columnNumber: message.location().columnNumber,\n          },')
review = once(review,
  '        state.merchant.variants[0].sale_available =\n          state.merchant.sale_available;',
  '        state.merchant.variants[0].sale_available =\n          state.merchant.sale_available;\n        state.merchant.trading_paused = body.available_count === 0;\n        state.merchant.variants[0].trading_paused = state.merchant.trading_paused;')
review = once(review,
  '  unlimited_supply: product.unlimited_supply,\n  inventory: inventorySnapshot(product),',
  '  unlimited_supply: product.unlimited_supply,\n  trading_paused: product.trading_paused,\n  inventory: inventorySnapshot(product),')
review = once(review,
  '            assert.equal(\n              reloadedProduct.unlimited_supply,',
  '            assert.equal(reloadedProduct.trading_paused, amount === 0);\n            assert.equal(reloadedProduct.variants[0].trading_paused, amount === 0);\n            assert.equal(\n              reloadedProduct.unlimited_supply,')
review = once(review,
  '    entry = require.resolve("playwright");',
  '    entry = process.env.PLAYWRIGHT_MODULE || require.resolve("playwright");')
review = once(review,
  '    executablePath: "/usr/bin/chromium",',
  '    executablePath: process.env.STOCK_REVIEW_CHROMIUM || undefined,')
review = once(review,
  '          fullPage: true,',
  '          fullPage: true,\n          animations: "disabled",')
save(reviewFile, review)

const sellerFile = 'apps/web/src/features/store/seller-page.tsx'
save(sellerFile, once(source(sellerFile),
  '<article key={product.id} className=\'space-y-4 py-6\'>',
  '<article key={product.id} data-store-product-id={product.id} className=\'space-y-4 py-6\'>'))

const { storeStockCopy } = await import(pathToFileURL(path.resolve('apps/web/scripts/store-stock-copy.mjs')))
const audit = []
for (const [locale, copy] of Object.entries(storeStockCopy)) {
  const file = `apps/web/src/i18n/locales/${locale}.json`
  const before = source(file, BASE)
  const original = JSON.parse(before)
  const pending = JSON.parse(source(file))
  for (const key of Object.keys(copy)) assert.ok(!Object.hasOwn(original.translation, key))
  // Preserve the base bytes and property order; append only the three new keys.
  const suffix = '\n  }\n}\n'
  assert.ok(before.endsWith(suffix))
  const entries = Object.entries(copy).map(([key, value]) => `    ${JSON.stringify(key)}: ${JSON.stringify(value)}`).join(',\n')
  const cleaned = before.slice(0, -suffix.length) + ',\n' + entries + suffix
  assert.deepEqual(JSON.parse(cleaned), { ...original, translation: { ...original.translation, ...copy } })
  audit.push({ locale, kept: Object.keys(copy), removedUnrelated: Object.keys(pending.translation).filter(key => !Object.hasOwn(original.translation, key) && !Object.hasOwn(copy, key)) })
  save(file, cleaned)
}
const reportFile = 'apps/web/src/i18n/locales/_reports/_sync-report.json'
save(reportFile, source(reportFile, BASE))
writeFileSync(path.join(out, 'translation-audit.json'), JSON.stringify(audit, null, 2) + '\n')

const quotaTest = `package model

import (
    "testing"

    "github.com/stretchr/testify/require"
)

func TestMerchantStoreStockQuotaCyclePreservesInventoryAndOrders(t *testing.T) {
    f := newStoreFixedTestFixture(t, "balance", "platform:waffo_pancake")
    var product MerchantStoreProduct
    require.NoError(t, DB.Where("seller_id = ? AND template = ?", f.seller.Id, "card-key").First(&product).Error)
    f.product = &product
    _, err := AddMerchantStoreStock(f.seller.Id, product.ID, []string{
        "QUOTA-CYCLE-THIRD", "QUOTA-CYCLE-FOURTH", "QUOTA-CYCLE-FIFTH",
        "QUOTA-CYCLE-SIXTH", "QUOTA-CYCLE-SEVENTH", "QUOTA-CYCLE-EIGHTH",
        "QUOTA-CYCLE-NINTH", "QUOTA-CYCLE-TENTH", "QUOTA-CYCLE-ELEVENTH",
    })
    require.NoError(t, err)
    paid, _, err := CreateMerchantStoreOrder(storeFixedCheckout(t, f, "quota-cycle-paid", "balance"))
    require.NoError(t, err)
    require.Equal(t, "paid", paid.Status)
    pending, _, err := CreateMerchantStoreOrder(storeFixedCheckout(t, f, "quota-cycle-reserved", "platform:waffo_pancake"))
    require.NoError(t, err)
    require.Equal(t, "pending", pending.Status)

    stocks := func() []MerchantStoreStock {
        t.Helper()
        var rows []MerchantStoreStock
        require.NoError(t, DB.Where("product_id = ?", product.ID).Order("id").Find(&rows).Error)
        return rows
    }
    orders := func() []MerchantStoreOrder {
        t.Helper()
        var rows []MerchantStoreOrder
        require.NoError(t, DB.Where("product_id = ?", product.ID).Order("id").Find(&rows).Error)
        return rows
    }
    originalStocks, originalOrders := stocks(), orders()
    require.Len(t, originalStocks, 11)
    require.Len(t, originalOrders, 2)
    var available, reserved, sold int
    for _, row := range originalStocks {
        require.NotEmpty(t, row.Ciphertext)
        switch row.State {
        case "available": available++
        case "reserved": reserved++
        case "delivered": sold++
        }
    }
    require.Equal(t, 9, available)
    require.Equal(t, 1, reserved)
    require.Equal(t, 1, sold)
    // Detect writes as well as final row differences, including ciphertext and
    // reservation ownership. All data belongs to the isolated test database.
    storeFixedNoStockWrites(t, DB)
    five, zero := int64(5), int64(0)
    for _, step := range []struct {
        name string
        remaining *int64
        purchasable int64
    }{{"unlimited", nil, 9}, {"five", &five, 4}, {"zero", &zero, 0}, {"restored", nil, 9}} {
        t.Run(step.name, func(t *testing.T) {
            require.NoError(t, SetMerchantStoreProductRemainingQuota(f.seller.Id, product.ID, step.remaining))
            public, err := GetPublicMerchantStoreProduct(product.ID)
            require.NoError(t, err)
            require.False(t, public.UnlimitedSupply)
            require.EqualValues(t, 9, public.InventoryAvailable)
            require.EqualValues(t, 1, public.PaidQuantity)
            require.EqualValues(t, 1, public.ReservedQuantity)
            require.Equal(t, step.purchasable, public.SaleAvailable)
            require.Equal(t, step.remaining != nil && *step.remaining == 0, public.TradingPaused)
            if step.remaining == nil {
                require.Nil(t, public.SaleLimit)
            } else {
                require.NotNil(t, public.SaleLimit)
                require.Equal(t, int64(1)+*step.remaining, *public.SaleLimit)
            }
            if step.purchasable == 0 {
                _, _, err = CreateMerchantStoreOrder(storeFixedCheckout(t, f, "quota-cycle-blocked", "balance"))
                require.ErrorIs(t, err, ErrMerchantStoreStock)
                require.Contains(t, public.DisplayTags, "trading_paused")
            } else {
                require.Contains(t, public.DisplayTags, "in_stock")
            }
            require.Equal(t, originalStocks, stocks(), "quota edits preserve every stock row and its content/state")
            require.Equal(t, originalOrders, orders(), "paid and reserved obligations survive every quota edit")
        })
    }
}
`
const testFile = 'apps/api-go/model/merchant_store_stock_quota_cycle_test.go'
save(testFile, quotaTest)
execFileSync('gofmt', ['-w', testFile])

// Keep the retained check manual. This preparation branch is never merged.
const workflowFile = '.github/workflows/store-stock-review.yml'
let workflow = source(workflowFile)
workflow = workflow.replace(/on:\n[\s\S]*?\npermissions:/, 'on:\n  workflow_dispatch:\n\npermissions:')
workflow = workflow.replace(/      - name: Record source and translation changes\n[\s\S]*?(?=      - uses: oven-sh\/setup-bun)/, '')
workflow = once(workflow,
  '      - name: Stock unit and catalogue interaction tests\n',
  '      - name: Check workflow boundaries and translations\n        run: |\n          node --test scripts/workflow-topology.test.mjs scripts/check-i18n.test.mjs\n          node scripts/check-i18n.mjs --base origin/main\n      - name: Stock unit and catalogue interaction tests\n')
workflow = once(workflow,
  '        run: bun test src/features/store/stock-status.test.ts src/features/store/catalogue-interactions.test.tsx',
  '        run: |\n          bun test src/features/store/stock-status.test.ts\n          bun test src/features/store/catalogue-interactions.test.tsx\n          bun test src/features/store/interactions.test.tsx')
workflow = once(workflow,
  '          browser_path=$(node -e "console.log(require(process.env.RUNNER_TEMP + \'/stock-browser/node_modules/playwright\').chromium.executablePath())")\n          sudo ln -sf "$browser_path" /usr/bin/chromium\n', '')
workflow = once(workflow,
  '          NODE_PATH: ${{ runner.temp }}/stock-browser/node_modules',
  '          PLAYWRIGHT_MODULE: ${{ runner.temp }}/stock-browser/node_modules/playwright/index.mjs')
save(workflowFile, workflow)

const formatFiles = [reviewFile, sellerFile, 'apps/web/scripts/store-stock-copy.mjs',
  'apps/web/src/features/store/stock-status.ts', 'apps/web/src/features/store/stock-status.test.ts',
  'apps/web/src/features/store/catalogue-tags.tsx']
execFileSync('bash', ['-euc', 'PATH="$PWD/node_modules/.bin:$PWD/../../node_modules/.bin:$PATH" oxfmt --write "$@"', 'stock-format', ...formatFiles.map(f => f.slice('apps/web/'.length))], { cwd: 'apps/web', stdio: 'inherit' })
formatFiles.forEach(file => changed.add(file))
execFileSync('node', ['--check', reviewFile], { stdio: 'inherit' })
const manifest = []
for (const file of changed) {
  const content = readFileSync(file, 'utf8')
  const bytes = Buffer.from(content)
  const expected = createHash('sha1').update(`blob ${bytes.length}\0`).update(bytes).digest('hex')
  const response = JSON.parse(execFileSync('gh', ['api', '--method', 'POST', 'repos/TokenNotIncluded/api.lmm.best/git/blobs', '--input', '-'], {
    input: JSON.stringify({ content, encoding: 'utf-8' }), encoding: 'utf8', maxBuffer: 1 << 20,
  }))
  assert.equal(response.sha, expected)
  manifest.push({ path: file, mode: '100644', type: 'blob', sha: expected })
}
writeFileSync(path.join(out, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n')
console.log(JSON.stringify(manifest, null, 2))
