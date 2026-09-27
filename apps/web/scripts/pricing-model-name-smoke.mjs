/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
// Real Router with fictional local APIs; no production requests or credentials.
import assert from 'node:assert/strict'

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ?? 'playwright')
const origin = process.env.PRICING_NAMES_ORIGIN ?? 'http://127.0.0.1:4176'
if (!['127.0.0.1', 'localhost', '[::1]'].includes(new URL(origin).hostname)) {
  throw new Error('Only local fixture origins are allowed')
}
const modelName = `local-provider/${'LongUnbrokenModelIdentifier'.repeat(4)}-preview`
const language = process.env.PRICING_NAMES_LANGUAGE ?? 'en'
const browser = await chromium.launch({
  headless: true,
  executablePath: process.env.CHROME_PATH || undefined,
})
const context = await browser.newContext({
  viewport: { width: 1440, height: 1000 },
  locale: 'en-US',
  reducedMotion: 'reduce',
  serviceWorkers: 'block',
  permissions: ['clipboard-read', 'clipboard-write'],
})
await context.addInitScript((language) => {
  localStorage.setItem('i18nextLng', language)
  localStorage.setItem('lmm:source-consent:v2', 'no')
}, language)
await context.route('**/*', async (route) => {
  const url = new URL(route.request().url())
  if (url.origin !== origin) return route.abort('blockedbyclient')
  if (!url.pathname.startsWith('/api/')) return route.continue()
  if (url.pathname === '/api/user/auth/refresh') {
    return route.fulfill({
      json: {
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
      },
    })
  }
  if (url.pathname === '/api/setup') {
    return route.fulfill({ json: { success: true, data: { status: true } } })
  }
  if (url.pathname === '/api/status') {
    return route.fulfill({
      json: {
        success: true,
        data: {
          system_name: 'Local fixture',
          price: 1,
          usd_exchange_rate: 1,
          assistant: { enabled: false },
        },
      },
    })
  }
  if (url.pathname === '/api/pricing') {
    return route.fulfill({
      json: {
        success: true,
        data: [
          {
            id: 1,
            model_name: modelName,
            vendor_id: 1,
            quota_type: 0,
            model_ratio: 1,
            completion_ratio: 2,
            enable_groups: ['default'],
            supported_endpoint_types: ['openai'],
          },
          {
            id: 2,
            model_name: 'claude-opus-4-6-thinking',
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
      },
    })
  }
  if (url.pathname === '/api/notice') {
    return route.fulfill({ json: { success: true, data: '' } })
  }
  return route.fulfill({
    json: { success: true, data: { groups: [], models: [] } },
  })
})
const page = await context.newPage()
page.setDefaultTimeout(15000)
const errors = []
page.on('pageerror', (error) => errors.push(error.message))
const checks = []
async function assertFullName(locator, label) {
  await locator.waitFor({ state: 'visible' })
  const dimensions = await locator.evaluate((el) => {
    const style = getComputedStyle(el)
    return {
      text: el.textContent,
      overflow: style.textOverflow,
      whiteSpace: style.whiteSpace,
      width: el.clientWidth,
      scrollWidth: el.scrollWidth,
    }
  })
  assert.equal(dimensions.text, modelName)
  assert.notEqual(
    dimensions.overflow,
    'ellipsis',
    `${label}: model name must not use ellipsis: ${JSON.stringify(dimensions)}`
  )
  assert.ok(
    dimensions.scrollWidth <= dimensions.width + 1,
    `${label}: name overflows its box: ${JSON.stringify(dimensions)}`
  )
}
try {
  await page.goto(`${origin}/pricing`, { waitUntil: 'networkidle' })
  const consent = page.getByRole('button', {
    name: /Do not collect|Don't collect|不收集/,
    exact: true,
  })
  if (await consent.isVisible()) await consent.click()
  if (language === 'zhCN') {
    await page
      .getByRole('button', { name: /Change language|更改语言/ })
      .first()
      .click()
    await page.getByRole('menuitem', { name: '简体中文', exact: true }).click()
    await page
      .getByTitle('复制', { exact: true })
      .first()
      .waitFor({ state: 'visible' })
  }
  for (const width of [320, 768, 1440]) {
    await page.setViewportSize({ width, height: 1000 })
    await assertFullName(
      page.getByRole('heading', { name: modelName, exact: true }),
      `card ${width}`
    )
    assert.ok(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1
      ),
      `page overflow at ${width}`
    )
    if (process.env.PRICING_NAMES_SCREENSHOTS && [320, 1440].includes(width)) {
      await page.screenshot({
        path: `${process.env.PRICING_NAMES_SCREENSHOTS}-${width}.png`,
        fullPage: true,
      })
    }
    const card = page
      .getByRole('heading', { name: modelName, exact: true })
      .locator('xpath=ancestor::div[contains(@class,"group")][1]')
    await card
      .getByTitle(language === 'en' ? 'Copy' : '复制', { exact: true })
      .click()
    assert.equal(
      await page.evaluate(() => navigator.clipboard.readText()),
      modelName
    )
    await card
      .getByRole('button', {
        name: language === 'en' ? 'Details' : '详情',
        exact: true,
      })
      .click()
    await page.locator('[role="dialog"] h1').waitFor({ state: 'visible' })
    assert.equal(
      await page.locator('[role="dialog"] h1').textContent(),
      modelName
    )
    await page.keyboard.press('Escape')
    await page.locator('[role="dialog"] h1').waitFor({ state: 'hidden' })
    checks.push(`card/details/copy ${width}`)
  }
  await page
    .getByRole('group', {
      name: language === 'en' ? 'View mode' : '视图模式',
      exact: true,
    })
    .getByRole('button')
    .nth(1)
    .click()
  for (const width of [320, 768, 1440]) {
    await page.setViewportSize({ width, height: 1000 })
    await assertFullName(
      page.locator('td').getByText(modelName, { exact: true }),
      `table ${width}`
    )
    assert.ok(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1
      ),
      `table page overflow at ${width}`
    )
    checks.push(`table ${width}`)
  }
  assert.deepEqual(errors, [])
  console.log(JSON.stringify({ result: 'PASS', checks }))
} catch (error) {
  console.log(
    JSON.stringify({
      result: 'FAIL',
      checks,
      errors,
      buttons: await page.locator('button').evaluateAll((nodes) =>
        nodes.map((n) => ({
          text: n.textContent,
          label: n.getAttribute('aria-label'),
          title: n.title,
        }))
      ),
    })
  )
  throw error
} finally {
  await browser.close()
}
