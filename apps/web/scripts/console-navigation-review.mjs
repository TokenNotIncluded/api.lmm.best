#!/usr/bin/env node
/*
Copyright (C) 2026 LIghtJUNction

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
*/
import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'

const { chromium } = await import(process.env.PLAYWRIGHT_MODULE ?? 'playwright')
// Never accept a production target: identities and responses are synthetic.
const baseUrl = 'http://127.0.0.1:4174'
const output = path.resolve(
  process.env.CONSOLE_REVIEW_OUTPUT ?? 'console-review'
)
await mkdir(output, { recursive: true })
const evidence = []
const browser = await chromium.launch({ headless: true })

async function captureFailure(page, error, name) {
  await page.screenshot({
    path: path.join(output, `${name}-failed.png`),
    fullPage: true,
    animations: 'disabled',
  })
  await writeFile(
    path.join(output, `${name}-failed.json`),
    JSON.stringify(
      {
        error: String(error),
        url: page.url(),
        tables: await page.locator('table').evaluateAll((tables) =>
          tables.map((table) => ({
            html: table.outerHTML,
            rect: table.getBoundingClientRect().toJSON(),
          }))
        ),
      },
      null,
      2
    )
  )
}

async function session(persona, viewport) {
  const context = await browser.newContext({
    viewport,
    locale: 'en-US',
    serviceWorkers: 'block',
  })
  await context.addInitScript(() => localStorage.setItem('i18nextLng', 'en'))
  const errors = []
  await context.route('**/*', async (route) => {
    const url = new URL(route.request().url())
    if (url.origin !== baseUrl) return route.abort('blockedbyclient')
    if (url.pathname === '/api/status') {
      return route.fulfill({
        json: {
          success: true,
          data: {
            system_name: 'LMM Best',
            logo: '/logo.png',
            assistant: { enabled: true },
            announcements_enabled: false,
          },
        },
      })
    }
    if (url.pathname.startsWith('/api/')) {
      errors.push(`Unexpected backend request: ${url.pathname}`)
      return route.abort('blockedbyclient')
    }
    return route.continue()
  })
  const page = await context.newPage()
  page.on('pageerror', (error) => errors.push(String(error)))
  try {
    await page.goto(baseUrl, { waitUntil: 'domcontentloaded' })
    await page.getByTestId('persona-debug-trigger').click()
    // The runtime starts as L0; selecting the current persona is a no-op.
    // Change identities first so the L0 case also exercises real navigation.
    if (persona === 'l0') {
      await page.getByTestId('persona-debug-option-l1').click()
      await page.waitForURL(/\/dashboard/)
      if (!(await page.getByTestId('persona-debug-panel').isVisible())) {
        await page.getByTestId('persona-debug-trigger').click()
      }
    }
    await page.getByTestId(`persona-debug-option-${persona}`).click()
    await page.waitForURL(
      persona === 'l0' ? /\/getting-started/ : /\/dashboard/
    )
    assert.equal(
      await page.locator('html').getAttribute('data-persona-debug'),
      'true'
    )
    // The development persona picker remains open after switching identities.
    await page.keyboard.press('Escape')
    await page.getByTestId('persona-debug-panel').waitFor({ state: 'hidden' })
    const rejectAnalytics = page.getByRole('button', {
      name: 'Do not collect',
      exact: true,
    })
    if (await rejectAnalytics.isVisible()) await rejectAnalytics.click()
    return { context, page, errors }
  } catch (error) {
    await captureFailure(page, error, `session-${persona}-${viewport.width}`)
    await context.close()
    throw error
  }
}

async function snapshot(page, name, errors) {
  await page.evaluate(() => document.fonts.ready)
  const dimensions = await page.evaluate(() => ({
    width: document.documentElement.clientWidth,
    scroll: document.documentElement.scrollWidth,
  }))
  assert.ok(
    dimensions.scroll <= dimensions.width + 1,
    JSON.stringify(dimensions)
  )
  assert.equal(
    await page.locator('[data-slot="sidebar-inset"] > header nav').count(),
    0,
    'parallel top navigation'
  )
  assert.deepEqual(errors, [], 'browser/runtime errors')
  assert.equal(
    await page.locator('[data-sonner-toast][data-type="error"]').count(),
    0,
    'handled application error toast'
  )
  assert.equal(
    await page.getByText(/PERSONA_DEBUG_UNMOCKED_REQUEST/).count(),
    0,
    'missing synthetic fixture'
  )
  await page.screenshot({
    path: path.join(output, `${name}.png`),
    fullPage: true,
    animations: 'disabled',
  })
  evidence.push({
    name,
    viewport: page.viewportSize(),
    dimensions,
    errors: [...errors],
  })
}

try {
  for (const viewport of [
    { width: 1440, height: 1000 },
    { width: 834, height: 1112 },
  ]) {
    const { context, page, errors } = await session('l1', viewport)
    try {
      const nav = page.getByRole('navigation', { name: 'Sidebar', exact: true })
      await nav.locator('a[href="/keys"]').click()
      await page.waitForURL(/\/keys/)
      await page
        .getByTestId('console-location')
        .filter({ hasText: 'API Keys' })
        .waitFor()
      const sidebar = await page
        .locator('[data-slot="sidebar-container"]')
        .boundingBox()
      const header = await page
        .locator('[data-slot="sidebar-inset"] > header')
        .boundingBox()
      assert.ok(sidebar && header && header.x >= sidebar.x + sidebar.width - 1)
      const ecosystem = page.getByTestId('console-section-forge')
      const trigger = ecosystem.getByRole('button', {
        name: 'Ecosystem',
        exact: true,
      })
      assert.equal(await trigger.getAttribute('aria-expanded'), 'false')
      await trigger.click()
      await ecosystem
        .locator('a[href="/tool-market"]')
        .waitFor({ state: 'visible' })
      await trigger.click()
      assert.equal(await trigger.getAttribute('aria-expanded'), 'false')
      await page.getByRole('button', { name: 'Search', exact: true }).click()
      await page.getByRole('dialog').waitFor({ state: 'visible' })
      await page.keyboard.press('Escape')
      await page.getByRole('dialog').waitFor({ state: 'hidden' })
      const empty = page.getByText('No API Keys Found', { exact: true })
      const emptyBox = await empty.boundingBox()
      const tableBox = await page
        .locator('[data-slot="data-table-viewport"]:visible')
        .boundingBox()
      assert.ok(emptyBox && tableBox)
      assert.ok(
        Math.abs(
          emptyBox.x + emptyBox.width / 2 - tableBox.x - tableBox.width / 2
        ) < 3,
        `Empty table content must be centered: ${JSON.stringify({ emptyBox, tableBox })}`
      )
      await snapshot(page, `keys-${viewport.width}`, errors)
      if (viewport.width === 1440) {
        await page.evaluate(() =>
          document.documentElement.classList.add('dark')
        )
        await snapshot(page, 'keys-1440-dark', errors)
        await page.evaluate(() =>
          document.documentElement.classList.remove('dark')
        )
        await nav.locator('a[href="/getting-started"]').click()
        await page.waitForURL(/\/getting-started/)
        await nav.waitFor({ state: 'visible' })
        await snapshot(page, 'assistant-navigation-1440', errors)
      }
    } catch (error) {
      await captureFailure(page, error, `view-${page.viewportSize().width}`)
      throw error
    } finally {
      await context.close()
    }
  }

  for (const width of [390, 320]) {
    const { context, page, errors } = await session('l1', {
      width,
      height: 844,
    })
    try {
      await page
        .getByRole('button', { name: 'Toggle Sidebar', exact: true })
        .click()
      const nav = page.getByRole('navigation', { name: 'Sidebar', exact: true })
      await nav.waitFor({ state: 'visible' })
      await snapshot(page, `navigation-${width}`, errors)
      await nav.locator('a[href="/keys"]').click()
      await page.waitForURL(/\/keys/)
      await nav.waitFor({ state: 'hidden' })
      await snapshot(page, `keys-${width}`, errors)
    } catch (error) {
      await captureFailure(page, error, `view-${page.viewportSize().width}`)
      throw error
    } finally {
      await context.close()
    }
  }

  const admin = await session('admin', { width: 1440, height: 1000 })
  try {
    const section = admin.page.getByTestId('console-section-admin')
    const trigger = section.getByRole('button', { name: 'Admin', exact: true })
    assert.equal(await trigger.getAttribute('aria-expanded'), 'false')
    await trigger.click()
    await section.locator('a[href="/channels"]').waitFor({ state: 'visible' })
    await snapshot(admin.page, 'admin-navigation', admin.errors)
  } catch (error) {
    await captureFailure(admin.page, error, 'admin')
    throw error
  } finally {
    await admin.context.close()
  }

  const l0 = await session('l0', { width: 390, height: 844 })
  try {
    assert.equal(await l0.page.locator('[data-slot="sidebar"]').count(), 0)
    assert.equal(
      await l0.page.getByRole('button', { name: 'Toggle Sidebar' }).count(),
      0
    )
    await snapshot(l0.page, 'l0-focused', l0.errors)
  } catch (error) {
    await captureFailure(l0.page, error, 'l0')
    throw error
  } finally {
    await l0.context.close()
  }
} finally {
  await writeFile(
    path.join(output, 'report.json'),
    JSON.stringify(evidence, null, 2)
  )
  await browser.close()
}
console.log(`Validated ${evidence.length} console views; artifacts: ${output}`)
