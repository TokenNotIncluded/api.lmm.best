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

const domWindow = new Window()
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { AuthArtPanel } = await import('./auth-art-panel')

const originalGet = api.get

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

async function renderArtwork() {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () =>
    root.render(
      <I18nextProvider i18n={i18n}>
        <AuthArtPanel />
      </I18nextProvider>
    )
  )
  return { container, root }
}

afterEach(() => {
  api.get = originalGet
  document.body.replaceChildren()
})
after(() => domWindow.close())

describe('AuthArtPanel optional game', () => {
  test('plays, hints, wins and restarts without network requests', async () => {
    let requests = 0
    api.get = (async () => {
      requests++
      throw new Error('game must stay local')
    }) as typeof api.get
    const rendered = await renderArtwork()
    try {
      const tiles = [
        ...rendered.container.querySelectorAll<HTMLButtonElement>(
          'button[aria-label^="Rotate tile"]'
        ),
      ]
      assert.equal(tiles.length, 25)
      assert.ok(tiles.every((button) => button.type === 'button'))
      const tile = tiles[10]
      const label = tile.getAttribute('aria-label')
      await act(async () => tile.click())
      assert.notEqual(tile.getAttribute('aria-label'), label)
      const hint = [
        ...rendered.container.querySelectorAll<HTMLButtonElement>('button'),
      ].find((button) => button.textContent?.includes('Hint'))
      assert.ok(hint)
      for (let move = 0; move < 76 && !hint.disabled; move++) {
        await act(async () => hint.click())
      }
      assert.equal(hint.disabled, true)
      assert.match(
        rendered.container.querySelector('[role="status"]')?.textContent ?? '',
        /Connected in/
      )
      const again = [
        ...rendered.container.querySelectorAll<HTMLButtonElement>('button'),
      ].find((button) => button.textContent?.includes('Play again'))
      assert.ok(again)
      await act(async () => again.click())
      assert.equal(hint.disabled, false)
      assert.equal(requests, 0)
      const active = rendered.container.querySelector<HTMLButtonElement>(
        '[role="grid"] button[tabindex="0"]'
      )
      assert.ok(active)
      await act(async () =>
        active.dispatchEvent(
          new domWindow.KeyboardEvent('keydown', {
            key: 'ArrowRight',
            bubbles: true,
          }) as unknown as Event
        )
      )
      assert.equal(
        rendered.container.querySelectorAll(
          '[role="grid"] button[tabindex="0"]'
        ).length,
        1
      )
    } finally {
      await act(async () => rendered.root.unmount())
    }
  })
})
