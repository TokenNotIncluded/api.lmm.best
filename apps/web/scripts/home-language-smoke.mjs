/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
// Run against the regular local dev entry; all API responses are fictional and external traffic is blocked.
import assert from 'node:assert/strict'

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ?? 'playwright')

const origin = process.env.HOME_LANGUAGE_ORIGIN ?? 'http://127.0.0.1:4175'
if (!['127.0.0.1', 'localhost', '[::1]'].includes(new URL(origin).hostname)) {
  throw new Error('Only local fixture origins are allowed')
}
const browser = await chromium.launch({
  headless: true,
  executablePath: process.env.CHROME_PATH || undefined,
})
const context = await browser.newContext({
  viewport: { width: 1440, height: 1000 },
  reducedMotion: 'reduce',
  locale: 'zh-CN',
  serviceWorkers: 'block',
})
await context.addInitScript(() => {
  if (!localStorage.getItem('i18nextLng')) {
    localStorage.setItem('i18nextLng', 'zhCN')
  }
  localStorage.setItem('lmm:source-consent:v2', 'no')
})
const requests = []
await context.route('**/*', async (route) => {
  const url = new URL(route.request().url())
  if (url.origin !== origin) return route.abort('blockedbyclient')
  if (!url.pathname.startsWith('/api/')) return route.continue()
  requests.push(`${route.request().method()} ${url.pathname}`)
  if (url.pathname === '/api/user/auth/refresh') {
    return route.fulfill({ status: 401, json: { success: false } })
  }
  if (url.pathname === '/api/setup') {
    return route.fulfill({ json: { success: true, data: { status: true } } })
  }
  if (url.pathname === '/api/status') {
    return route.fulfill({
      json: {
        success: true,
        data: {
          system_name: 'Local language test',
          register_enabled: true,
          assistant: { enabled: false },
          backend_capabilities: { bounty_public_read: false },
        },
      },
    })
  }
  if (url.pathname === '/api/notice') {
    return route.fulfill({ json: { success: true, data: '' } })
  }
  return route.fulfill({ json: { success: true, data: [] } })
})
const page = await context.newPage()
page.setDefaultTimeout(10000)
const pageErrors = []
page.on('pageerror', (error) => pageErrors.push(error.message))
const assertHeadline = async (text) => {
  await page.locator('#lmm-home-title').waitFor({ state: 'attached' })
  await page.waitForFunction(
    (expected) =>
      document.querySelector('#lmm-home-title')?.textContent === expected,
    text,
    { timeout: 5000 }
  )
  const actual = await page.locator('#lmm-home-title').innerText()
  assert.equal(actual.replaceAll('\n', ''), text)
}
try {
  await page.goto(`${origin}/`, { waitUntil: 'networkidle' })
  await assertHeadline('把时间，留给下一个想法。')
  const rejectConsent = page.getByRole('button', {
    name: '不收集',
    exact: true,
  })
  if (await rejectConsent.isVisible()) await rejectConsent.click()
  for (const [label, code, title] of [
    ['繁體中文', 'zhTW', '把時間，留給下一個想法。'],
    ['English', 'en', 'Make room for your next idea.'],
    ['简体中文', 'zhCN', '把时间，留给下一个想法。'],
  ]) {
    await page
      .getByRole('button', {
        name: /更改语言|變更語言|Change language|更改語言/,
      })
      .first()
      .click()
    await page.getByRole('menuitem', { name: label, exact: true }).click()
    await assertHeadline(title)
    assert.equal(
      await page.evaluate(() => localStorage.getItem('i18nextLng')),
      code
    )
    await page.reload({ waitUntil: 'networkidle' })
    await assertHeadline(title)
  }
  await page.locator('a[href="/challenges"]').first().click()
  await page.waitForURL('**/challenges')
  assert.equal(
    await page.evaluate(() => localStorage.getItem('i18nextLng')),
    'zhCN'
  )
  await page.goBack({ waitUntil: 'networkidle' })
  await assertHeadline('把时间，留给下一个想法。')
  assert.deepEqual(pageErrors, [])
  console.log(
    JSON.stringify({
      result: 'PASS',
      checks: [
        'saved zhCN homepage',
        'zhTW/en/zhCN UI switching',
        'reload keeps selection',
        'other route and back keeps selection',
      ],
      requests,
    })
  )
} catch (error) {
  console.log(
    JSON.stringify({
      result: 'FAIL',
      url: page.url(),
      buttons: await page.locator('button').evaluateAll((nodes) =>
        nodes.map((n) => ({
          text: n.textContent,
          aria: n.getAttribute('aria-label'),
        }))
      ),
      headline: await page
        .locator('#lmm-home-title')
        .textContent()
        .catch(() => null),
      saved: await page
        .evaluate(() => localStorage.getItem('i18nextLng'))
        .catch(() => null),
      pageErrors,
      requests,
    })
  )
  throw error
} finally {
  await browser.close()
}
