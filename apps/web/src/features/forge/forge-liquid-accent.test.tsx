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

import type {
  ForgeLiquidAccentEnvironment,
  ForgeLiquidAccentRuntimeLoader,
} from './forge-liquid-accent'
import type { ForgeLiquidAccentCapabilities } from './forge-liquid-accent-policy'

const domWindow = new Window()
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'SVGElement',
  'Node',
  'Element',
  'DOMRect',
  'MutationObserver',
  'ResizeObserver',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.defineProperties(globalThis, {
  cancelAnimationFrame: {
    configurable: true,
    value: domWindow.cancelAnimationFrame.bind(domWindow),
  },
  getComputedStyle: {
    configurable: true,
    value: domWindow.getComputedStyle.bind(domWindow),
  },
  requestAnimationFrame: {
    configurable: true,
    value: domWindow.requestAnimationFrame.bind(domWindow),
  },
})

const { StrictMode, act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { ForgeLiquidAccent } = await import('./forge-liquid-accent')
const { ForgeLiquidAccentRuntime } =
  await import('./forge-liquid-accent-runtime')
const { shouldEnableForgeLiquidAccent } =
  await import('./forge-liquid-accent-policy')

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const enabledCapabilities: ForgeLiquidAccentCapabilities = {
  coarsePointer: false,
  forcedColors: false,
  narrowViewport: false,
  reducedMotion: false,
  supportsResizeObserver: true,
  supportsSvgFilters: true,
}

function createEnvironment(capabilities: ForgeLiquidAccentCapabilities) {
  const listeners = new Set<() => void>()
  let subscribeCount = 0
  let unsubscribeCount = 0
  const environment: ForgeLiquidAccentEnvironment = {
    read: () => capabilities,
    subscribe: (listener) => {
      subscribeCount += 1
      listeners.add(listener)
      return () => {
        unsubscribeCount += 1
        listeners.delete(listener)
      }
    },
  }
  return {
    environment,
    listenerCount: () => listeners.size,
    subscribeCount: () => subscribeCount,
    unsubscribeCount: () => unsubscribeCount,
  }
}

async function renderAccent(
  environment: ForgeLiquidAccentEnvironment,
  loadRuntime: ForgeLiquidAccentRuntimeLoader
) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <ForgeLiquidAccent environment={environment} loadRuntime={loadRuntime} />
    )
    await Promise.resolve()
    await Promise.resolve()
  })
  return { container, root }
}

afterEach(() => document.body.replaceChildren())
after(() => domWindow.close())

describe('ForgeLiquidAccent capability policy', () => {
  test('requires every desktop rendering capability', () => {
    assert.equal(shouldEnableForgeLiquidAccent(enabledCapabilities), true)

    for (const blocked of [
      'coarsePointer',
      'forcedColors',
      'narrowViewport',
      'reducedMotion',
    ] as const) {
      assert.equal(
        shouldEnableForgeLiquidAccent({
          ...enabledCapabilities,
          [blocked]: true,
        }),
        false,
        blocked
      )
    }
    for (const unsupported of [
      'supportsResizeObserver',
      'supportsSvgFilters',
    ] as const) {
      assert.equal(
        shouldEnableForgeLiquidAccent({
          ...enabledCapabilities,
          [unsupported]: false,
        }),
        false,
        unsupported
      )
    }
  })
})

describe('ForgeLiquidAccent', () => {
  test('keeps the static fallback without loading code on blocked devices', async () => {
    const harness = createEnvironment({
      ...enabledCapabilities,
      reducedMotion: true,
    })
    let loads = 0
    const rendered = await renderAccent(harness.environment, async () => {
      loads += 1
      return { ForgeLiquidAccentRuntime: () => <span data-runtime /> }
    })

    const accent = rendered.container.querySelector(
      '[data-forge-liquid-accent]'
    )
    assert.equal(accent?.getAttribute('data-forge-liquid-accent'), 'static')
    assert.equal(accent?.getAttribute('aria-hidden'), 'true')
    assert.equal(loads, 0)

    await act(async () => rendered.root.unmount())
  })

  test('loads an isolated decorative runtime only on capable desktops', async () => {
    const harness = createEnvironment(enabledCapabilities)
    const rendered = await renderAccent(harness.environment, async () => ({
      ForgeLiquidAccentRuntime: () => <span data-liquid-runtime='ready' />,
    }))

    const accent = rendered.container.querySelector(
      '[data-forge-liquid-accent="enhanced"]'
    )
    assert.ok(accent)
    assert.ok(accent.querySelector('[data-liquid-runtime="ready"]'))
    assert.equal(
      accent.querySelector('a, button, input, select, textarea, [tabindex]'),
      null
    )

    await act(async () => rendered.root.unmount())
  })

  test('contains loader failure and restores the static fallback', async () => {
    const harness = createEnvironment(enabledCapabilities)
    const rendered = await renderAccent(harness.environment, async () => {
      throw new Error('optional chunk unavailable')
    })

    assert.ok(
      rendered.container.querySelector('[data-forge-liquid-accent="static"]')
    )
    await act(async () => rendered.root.unmount())
  })

  test('balances capability listeners through StrictMode remounts', async () => {
    const harness = createEnvironment({
      ...enabledCapabilities,
      reducedMotion: true,
    })
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)

    await act(async () => {
      root.render(
        <StrictMode>
          <ForgeLiquidAccent
            environment={harness.environment}
            loadRuntime={async () => ({
              ForgeLiquidAccentRuntime: () => null,
            })}
          />
        </StrictMode>
      )
    })
    await act(async () => root.unmount())

    assert.equal(harness.listenerCount(), 0)
    assert.equal(harness.subscribeCount(), harness.unsubscribeCount())
  })

  test('renders the reviewed package through the runtime seam', async () => {
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)

    await act(async () => root.render(<ForgeLiquidAccentRuntime />))
    assert.ok(container.querySelector('svg[aria-hidden="true"]'))
    assert.ok(container.querySelector('filter feGaussianBlur'))
    assert.equal(container.querySelectorAll('span').length, 3)

    await act(async () => root.unmount())
  })

  test('keeps the third-party package behind the runtime seam', () => {
    const host = readFileSync(
      new URL('./forge-liquid-accent.tsx', import.meta.url),
      'utf8'
    )
    const runtime = readFileSync(
      new URL('./forge-liquid-accent-runtime.tsx', import.meta.url),
      'utf8'
    )
    const styles = readFileSync(
      new URL('./forge-liquid-accent.module.css', import.meta.url),
      'utf8'
    )

    assert.equal(host.includes("from 'liquid-gooey'"), false)
    assert.equal(runtime.includes("from 'liquid-gooey'"), true)
    assert.equal(runtime.includes('effect='), false)
    assert.equal(runtime.includes('dissolve='), false)
    assert.equal(runtime.includes('waviness={0}'), true)
    assert.equal(styles.includes('(forced-colors: active)'), true)
    assert.equal(styles.includes('(pointer: coarse)'), true)
    assert.equal(styles.includes('@keyframes'), false)
  })
})
