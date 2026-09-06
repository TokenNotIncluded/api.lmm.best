/*
Copyright (C) 2023-2026 QuantumNous

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

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { after, afterEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({ url: 'https://console.example.test/' })
for (const key of ['window', 'document', 'HTMLAnchorElement'] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { openExternalUrl, openResolvedExternalUrl } =
  await import('./external-navigation')

const originalWindowOpen = window.open
const originalAnchorClick = HTMLAnchorElement.prototype.click

function createPopup() {
  let closeCount = 0
  let focusCount = 0
  const popup = {
    closed: false,
    close: () => {
      closeCount += 1
      popup.closed = true
    },
    focus: () => {
      focusCount += 1
    },
    location: { href: '' },
    opener: {} as Window | null,
  }

  return {
    popup,
    closeCount: () => closeCount,
    focusCount: () => focusCount,
  }
}

afterEach(() => {
  window.open = originalWindowOpen
  HTMLAnchorElement.prototype.click = originalAnchorClick
  document.body.replaceChildren()
})

after(() => domWindow.close())

describe('openResolvedExternalUrl', () => {
  test('navigates and focuses a popup reserved before resolving the URL', async () => {
    const reserved = createPopup()
    let fallbackClicks = 0
    window.open = (() =>
      reserved.popup as unknown as Window) as unknown as typeof window.open
    HTMLAnchorElement.prototype.click = () => {
      fallbackClicks += 1
    }

    const opened = await openResolvedExternalUrl(async () => {
      await Promise.resolve()
      return 'https://chat.example.test/session'
    })

    assert.equal(opened, true)
    assert.equal(
      reserved.popup.location.href,
      'https://chat.example.test/session'
    )
    assert.equal(reserved.popup.opener, null)
    assert.equal(reserved.focusCount(), 1)
    assert.equal(reserved.closeCount(), 0)
    assert.equal(fallbackClicks, 0)
  })

  test('falls back to current-tab navigation when the popup is blocked', async () => {
    const navigations: string[] = []
    window.open = (() => null) as typeof window.open
    HTMLAnchorElement.prototype.click = function () {
      navigations.push(this.href)
    }

    const opened = await openResolvedExternalUrl(
      () => 'ccswitch://v1/import?resource=provider&app=claude'
    )

    assert.equal(opened, true)
    assert.deepEqual(navigations, [
      'ccswitch://v1/import?resource=provider&app=claude',
    ])
    assert.equal(document.querySelector('a'), null)
  })

  test('falls back when the reserved popup becomes unavailable', async () => {
    const reserved = createPopup()
    const navigations: string[] = []
    window.open = (() =>
      reserved.popup as unknown as Window) as unknown as typeof window.open
    HTMLAnchorElement.prototype.click = function () {
      navigations.push(this.href)
    }

    const opened = await openResolvedExternalUrl(async () => {
      reserved.popup.closed = true
      return 'https://chat.example.test/session'
    })

    assert.equal(opened, true)
    assert.deepEqual(navigations, ['https://chat.example.test/session'])
    assert.equal(reserved.popup.location.href, '')
  })

  test('closes an unused popup when no URL can be prepared', async () => {
    const reserved = createPopup()
    window.open = (() =>
      reserved.popup as unknown as Window) as unknown as typeof window.open

    const opened = await openResolvedExternalUrl(() => null)

    assert.equal(opened, false)
    assert.equal(reserved.closeCount(), 1)
    assert.equal(reserved.popup.location.href, '')
  })

  test('closes an unused popup and preserves resolver errors', async () => {
    const reserved = createPopup()
    window.open = (() =>
      reserved.popup as unknown as Window) as unknown as typeof window.open

    await assert.rejects(
      openResolvedExternalUrl(() => {
        throw new Error('credential lookup failed')
      }),
      /credential lookup failed/
    )

    assert.equal(reserved.closeCount(), 1)
    assert.equal(reserved.popup.location.href, '')
  })

  test('removes the fallback anchor when current-tab navigation fails', async () => {
    window.open = (() => null) as typeof window.open
    HTMLAnchorElement.prototype.click = () => {
      throw new Error('navigation rejected')
    }

    const opened = await openResolvedExternalUrl(
      () => 'https://chat.example.test/session'
    )

    assert.equal(opened, false)
    assert.equal(document.querySelector('a'), null)
  })
})

describe('openExternalUrl', () => {
  test('uses the same blocked-popup fallback for a ready URL', async () => {
    const navigations: string[] = []
    window.open = (() => null) as typeof window.open
    HTMLAnchorElement.prototype.click = function () {
      navigations.push(this.href)
    }

    const opened = await openExternalUrl('https://docs.example.test/guide')

    assert.equal(opened, true)
    assert.deepEqual(navigations, ['https://docs.example.test/guide'])
  })
})
