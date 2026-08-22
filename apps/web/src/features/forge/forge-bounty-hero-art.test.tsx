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
import { readFileSync } from 'node:fs'
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
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { ForgeBountyHeroArt } = await import('./forge-bounty-hero-art')

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

async function renderArtwork() {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => root.render(<ForgeBountyHeroArt />))
  return { container, root }
}

afterEach(() => document.body.replaceChildren())
after(() => domWindow.close())

describe('ForgeBountyHeroArt', () => {
  test('renders one restrained Anthropic editorial metaphor', async () => {
    const rendered = await renderArtwork()
    const wrapper = rendered.container.querySelector(
      '[data-forge-bounty-art="editorial"]'
    )
    assert.ok(wrapper)
    assert.equal(wrapper.querySelectorAll('svg[role="img"]').length, 1)
    assert.equal(wrapper.querySelectorAll('img').length, 0)
    assert.equal(
      wrapper.querySelectorAll('g[aria-hidden="true"] path').length,
      4
    )
    assert.equal(
      wrapper.querySelectorAll('g[aria-hidden="true"] circle').length,
      1
    )

    await act(async () => rendered.root.unmount())
  })

  test('does not retain the hidden technical network or animation loop', () => {
    const source = readFileSync(
      new URL('./forge-bounty-hero-art.tsx', import.meta.url),
      'utf8'
    )
    const styles = readFileSync(
      new URL('./forge-bounty-hero-art.module.css', import.meta.url),
      'utf8'
    )
    const shellStyles = readFileSync(
      new URL('./forge-public-shell.css', import.meta.url),
      'utf8'
    )
    const homeSource = readFileSync(
      new URL('./forge-home.tsx', import.meta.url),
      'utf8'
    )

    for (const forbidden of [
      'data-fluid-id',
      'data-fluid-node',
      'data-fluid-path',
      'requestAnimationFrame',
      'onPointerMove',
      'legacyField',
      'CONTRIBUTION_PATHS',
      'ivoryCarrier',
      'paperContour',
      'coreMark',
    ]) {
      assert.equal(source.includes(forbidden), false, forbidden)
      assert.equal(styles.includes(forbidden), false, forbidden)
    }
    assert.equal(styles.includes('gradient'), false)
    assert.equal(styles.includes('stroke-width: 12'), true)
    assert.equal(styles.includes('stroke-width: 8'), false)
    // The public surface keeps its paper/ink token bridge: the home page's
    // black-and-white editorial look is defined there, independent of the
    // active theme preset.
    assert.equal(shellStyles.includes('.forge-surface {'), true)
    assert.equal(
      shellStyles.includes('--background: var(--forge-paper-light);'),
      true
    )
    assert.equal(homeSource.includes('/forge-collaboration.webp'), false)
    assert.equal(homeSource.includes('before:bg-[#141413]'), false)
    assert.equal(homeSource.includes('before:bg-foreground'), false)
    assert.equal(homeSource.includes('Describe what you need...'), true)
  })
})
