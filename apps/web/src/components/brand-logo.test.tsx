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
import { after, describe, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window()
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Node',
  'Element',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { BrandLogo } = await import('./brand-logo')

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

describe('BrandLogo', () => {
  after(() => domWindow.close())

  test('renders the default logo as an inline decorative SVG without an image URL', async () => {
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => root.render(<BrandLogo src='/logo.png' />))

    const mark = container.querySelector('svg')
    assert.ok(mark)
    assert.equal(mark.getAttribute('aria-hidden'), 'true')
    assert.equal(container.querySelector('img'), null)
    assert.equal(container.innerHTML.includes('/logo.png'), false)

    await act(async () => root.unmount())
  })

  test('keeps a tenant logo as an accessible image', async () => {
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () =>
      root.render(
        <BrandLogo
          src='https://tenant.example/logo.svg'
          alt='Example tenant logo'
        />
      )
    )

    const image = container.querySelector('img')
    assert.ok(image)
    assert.equal(image.getAttribute('src'), 'https://tenant.example/logo.svg')
    assert.equal(image.getAttribute('alt'), 'Example tenant logo')
    assert.equal(container.querySelector('svg'), null)

    await act(async () => root.unmount())
  })

  test('rejects unsafe logo URLs and falls back to the inline mark', async () => {
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () =>
      root.render(<BrandLogo src='javascript:alert(1)' alt='Unsafe logo' />)
    )

    assert.equal(container.querySelector('img'), null)
    assert.ok(container.querySelector('svg'))

    await act(async () => root.unmount())
  })
})
