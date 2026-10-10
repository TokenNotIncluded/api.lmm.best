/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'

// Base UI marks a popup visible before its scale-in animation has finished.
async function settlePopup(locator) {
  await locator.waitFor({ state: 'visible' })
  await locator.evaluate((element) =>
    Promise.all(element.getAnimations().map((animation) => animation.finished))
  )
}

async function withinViewport(page, locator, name) {
  const box = await locator.boundingBox()
  const viewport = page.viewportSize()
  assert.ok(box && viewport, `${name} is visible`)
  assert.ok(
    box.x >= -1 &&
      box.y >= -1 &&
      box.x + box.width <= viewport.width + 1 &&
      box.y + box.height <= viewport.height + 1,
    `${name} must fit: ${JSON.stringify({ box, viewport })}`
  )
}

/** Uses the caller's loopback-only synthetic session; never creates API traffic. */
export async function reviewMobileHeader(page, snapshot, errors) {
  const width = page.viewportSize().width
  const more = page.getByTestId('header-more-actions')
  await more.waitFor({ state: 'visible' })
  const headerSize = await page
    .locator('[data-slot="sidebar-inset"] > header')
    .evaluate((element) => ({
      width: element.clientWidth,
      scrollWidth: element.scrollWidth,
    }))
  assert.ok(headerSize.scrollWidth <= headerSize.width + 1, 'header overflow')
  for (const control of [
    more,
    page.getByTestId('header-assistant-launcher'),
    page.getByTestId('mobile-account-balance'),
    page.getByRole('button', { name: 'Toggle Sidebar', exact: true }),
  ]) {
    const box = await control.boundingBox()
    assert.ok(box && box.width >= 44 && box.height >= 44, '44px touch target')
    await withinViewport(page, control, 'header action')
  }
  await snapshot(page, `mobile-header-${width}`, errors)

  await more.click()
  const menu = page.getByTestId('header-tools-menu')
  await settlePopup(menu)
  await withinViewport(page, menu, 'more actions')
  const shop = menu.getByTestId('header-store-link')
  assert.equal(await shop.getAttribute('href'), '/store')
  const shopBox = await shop.boundingBox()
  assert.ok(
    shopBox && shopBox.width >= 44 && shopBox.height >= 44,
    `Shop touch target: ${JSON.stringify(shopBox)}`
  )
  await snapshot(page, `mobile-tools-${width}`, errors)
  await page.evaluate(() => document.documentElement.classList.add('dark'))
  await snapshot(page, `mobile-tools-${width}-dark`, errors)
  await page.evaluate(() => document.documentElement.classList.remove('dark'))

  // A nested notification popover must remain usable inside the new menu.
  await menu
    .getByTestId('header-tool-notifications')
    .getByRole('button', { name: 'Notifications', exact: true })
    .click()
  const notifications = page.locator('[data-slot="popover-content"]').filter({
    has: page.getByRole('heading', { name: 'Notifications', exact: true }),
  })
  await settlePopup(notifications)
  await withinViewport(page, notifications, 'notifications')
  const timeline = notifications.getByRole('tab', {
    name: 'Timeline',
    exact: true,
  })
  await timeline.click()
  assert.equal(await timeline.getAttribute('aria-selected'), 'true')
  await notifications.waitFor({ state: 'visible' })
  await snapshot(page, `mobile-notifications-${width}`, errors)
  await page.keyboard.press('Escape')
  await notifications.waitFor({ state: 'hidden' })
  // Close nested overlays in their native order, then restore the header trigger.
  if (await menu.isVisible()) await page.keyboard.press('Escape')
  await menu.waitFor({ state: 'hidden' })
  await page.waitForFunction(
    () =>
      document.activeElement?.getAttribute('data-testid') ===
      'header-more-actions'
  )
  assert.equal(
    await more.evaluate((element) => element === document.activeElement),
    true
  )

  if (width === 390) {
    // Rotation must not restore a stale notification panel or a stale more menu.
    await more.click()
    await menu
      .getByTestId('header-tool-notifications')
      .getByRole('button', { name: 'Notifications', exact: true })
      .click()
    await notifications.waitFor({ state: 'visible' })
    await page.setViewportSize({ width: 834, height: 1112 })
    await more.waitFor({ state: 'hidden' })
    await notifications.waitFor({ state: 'hidden' })
    await page.getByTestId('header-store-link').waitFor({ state: 'visible' })
    const desktopNotifications = page.getByRole('button', {
      name: 'Notifications',
      exact: true,
    })
    assert.equal(
      await desktopNotifications.getAttribute('aria-expanded'),
      'false'
    )
    await desktopNotifications.click()
    await settlePopup(notifications)
    await page.keyboard.press('Escape')
    await notifications.waitFor({ state: 'hidden' })
    await page.setViewportSize({ width: 390, height: 844 })
    await more.waitFor({ state: 'visible' })
    await menu.waitFor({ state: 'hidden' })
    await more.click()
    await menu.waitFor({ state: 'visible' })
    await notifications.waitFor({ state: 'hidden' })
    await page.keyboard.press('Escape')
    await menu.waitFor({ state: 'hidden' })
  }
}
