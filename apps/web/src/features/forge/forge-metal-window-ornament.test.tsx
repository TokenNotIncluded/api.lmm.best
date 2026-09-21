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
  ForgeMetalWindowOrnamentEnvironment,
  ForgeMetalWindowOrnamentRuntimeLoader,
} from './forge-metal-window-ornament'
import type { ForgeMetalWindowOrnamentCapabilities } from './forge-metal-window-ornament-policy'

const domWindow = new Window()
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLCanvasElement',
  'SVGElement',
  'Node',
  'Element',
  'DOMRect',
  'MutationObserver',
  'ResizeObserver',
  'IntersectionObserver',
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
const { ForgeMetalWindowOrnament } =
  await import('./forge-metal-window-ornament')
const { isAppleWebKitBrowser, shouldEnableForgeMetalWindowOrnament } =
  await import('./forge-metal-window-ornament-policy')

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const enabledCapabilities: ForgeMetalWindowOrnamentCapabilities = {
  appleWebKit: false,
  coarsePointer: false,
  forcedColors: false,
  narrowViewport: false,
  reducedMotion: false,
  saveData: false,
  supportsAnimationFrame: true,
  supportsCanvas2D: true,
  supportsIntersectionObserver: true,
  supportsResizeObserver: true,
  supportsRoundRect: true,
  supportsWebGL: true,
}

function createEnvironment(capabilities: ForgeMetalWindowOrnamentCapabilities) {
  const listeners = new Set<() => void>()
  let subscribeCount = 0
  let unsubscribeCount = 0
  const environment: ForgeMetalWindowOrnamentEnvironment = {
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

async function renderOrnament(
  environment: ForgeMetalWindowOrnamentEnvironment,
  loadRuntime: ForgeMetalWindowOrnamentRuntimeLoader
) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container, { onCaughtError: () => undefined })
  await act(async () => {
    root.render(
      <ForgeMetalWindowOrnament
        environment={environment}
        loadRuntime={loadRuntime}
      />
    )
    await Promise.resolve()
    await Promise.resolve()
  })
  return { container, root }
}

function assertCompleteFallback(container: Element) {
  assert.ok(container.querySelector('[data-forge-metal-window-fallback]'))
  assert.equal(
    container.querySelectorAll('[data-forge-metal-window-dot]').length,
    3
  )
}

afterEach(() => document.body.replaceChildren())
after(() => domWindow.close())

describe('ForgeMetalWindowOrnament capability policy', () => {
  test('requires every reviewed rendering capability', () => {
    assert.equal(
      shouldEnableForgeMetalWindowOrnament(enabledCapabilities),
      true
    )

    for (const blocked of [
      'appleWebKit',
      'coarsePointer',
      'forcedColors',
      'narrowViewport',
      'reducedMotion',
      'saveData',
    ] as const) {
      assert.equal(
        shouldEnableForgeMetalWindowOrnament({
          ...enabledCapabilities,
          [blocked]: true,
        }),
        false,
        blocked
      )
    }

    for (const unsupported of [
      'supportsAnimationFrame',
      'supportsCanvas2D',
      'supportsIntersectionObserver',
      'supportsResizeObserver',
      'supportsRoundRect',
      'supportsWebGL',
    ] as const) {
      assert.equal(
        shouldEnableForgeMetalWindowOrnament({
          ...enabledCapabilities,
          [unsupported]: false,
        }),
        false,
        unsupported
      )
    }
  })

  test('blocks Safari and every iOS browser without blocking desktop Chromium', () => {
    assert.equal(
      isAppleWebKitBrowser(
        'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Version/18.0 Safari/605.1.15'
      ),
      true
    )
    assert.equal(
      isAppleWebKitBrowser(
        'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 CriOS/128.0 Mobile/15E148 Safari/604.1'
      ),
      true
    )
    assert.equal(
      isAppleWebKitBrowser(
        'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/128.0.0.0 Safari/537.36'
      ),
      false
    )
  })

  test('fails closed when capability property reads throw', () => {
    const throwingCapabilities = new Proxy(enabledCapabilities, {
      get() {
        throw new Error('blocked capability read')
      },
    })

    assert.equal(
      shouldEnableForgeMetalWindowOrnament(throwingCapabilities),
      false
    )
  })
})

describe('ForgeMetalWindowOrnament', () => {
  test('does not load code when any policy branch blocks enhancement', async () => {
    for (const blocked of [
      'appleWebKit',
      'coarsePointer',
      'forcedColors',
      'narrowViewport',
      'reducedMotion',
      'saveData',
    ] as const) {
      const harness = createEnvironment({
        ...enabledCapabilities,
        [blocked]: true,
      })
      let loads = 0
      const rendered = await renderOrnament(harness.environment, async () => {
        loads += 1
        return { ForgeMetalWindowOrnamentRuntime: () => null }
      })

      assert.equal(loads, 0, blocked)
      assertCompleteFallback(rendered.container)
      await act(async () => rendered.root.unmount())
    }
  })

  test('keeps the complete fallback mounted while the runtime loads', async () => {
    const harness = createEnvironment(enabledCapabilities)
    let resolveRuntime: (
      module: Awaited<ReturnType<ForgeMetalWindowOrnamentRuntimeLoader>>
    ) => void = () => undefined
    const pendingRuntime = new Promise<
      Awaited<ReturnType<ForgeMetalWindowOrnamentRuntimeLoader>>
    >((resolve) => {
      resolveRuntime = resolve
    })
    const rendered = await renderOrnament(
      harness.environment,
      async () => pendingRuntime
    )

    const ornament = rendered.container.querySelector(
      '[data-forge-metal-window-ornament="static"]'
    )
    assert.ok(ornament)
    assert.equal(ornament.getAttribute('aria-hidden'), 'true')
    assert.equal(ornament.hasAttribute('inert'), true)
    assertCompleteFallback(ornament)

    await act(async () => rendered.root.unmount())
    resolveRuntime({ ForgeMetalWindowOrnamentRuntime: () => null })
  })

  test('contains loader failure and preserves the fallback', async () => {
    const harness = createEnvironment(enabledCapabilities)
    const rendered = await renderOrnament(harness.environment, async () => {
      throw new Error('optional chunk unavailable')
    })

    const ornament = rendered.container.querySelector(
      '[data-forge-metal-window-ornament="static"]'
    )
    assert.ok(ornament)
    assertCompleteFallback(ornament)
    await act(async () => rendered.root.unmount())
  })

  test('contains runtime render failure and preserves the fallback', async () => {
    const harness = createEnvironment(enabledCapabilities)
    const rendered = await renderOrnament(harness.environment, async () => ({
      ForgeMetalWindowOrnamentRuntime: () => {
        throw new Error('optional runtime render failure')
      },
    }))

    const ornament = rendered.container.querySelector(
      '[data-forge-metal-window-ornament="static"]'
    )
    assert.ok(ornament)
    assertCompleteFallback(ornament)
    await act(async () => rendered.root.unmount())
  })

  test('fails closed when environment reads and subscriptions throw', async () => {
    let loads = 0
    const rendered = await renderOrnament(
      {
        read: () => {
          throw new Error('privacy guard rejected the read')
        },
        subscribe: () => {
          throw new Error('privacy guard rejected the subscription')
        },
      },
      async () => {
        loads += 1
        return { ForgeMetalWindowOrnamentRuntime: () => null }
      }
    )

    assert.equal(loads, 0)
    assertCompleteFallback(rendered.container)
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
          <ForgeMetalWindowOrnament
            environment={harness.environment}
            loadRuntime={async () => ({
              ForgeMetalWindowOrnamentRuntime: () => null,
            })}
          />
        </StrictMode>
      )
    })
    await act(async () => root.unmount())

    assert.equal(harness.listenerCount(), 0)
    assert.equal(harness.subscribeCount(), harness.unsubscribeCount())
  })

  test('has no interactive or focusable descendants after enhancement', async () => {
    const harness = createEnvironment(enabledCapabilities)
    const rendered = await renderOrnament(harness.environment, async () => ({
      ForgeMetalWindowOrnamentRuntime: () => (
        <span aria-hidden='true' data-metal-runtime inert />
      ),
    }))

    const ornament = rendered.container.querySelector(
      '[data-forge-metal-window-ornament="enhanced"]'
    )
    assert.ok(ornament)
    assert.ok(ornament.querySelector('[data-metal-runtime]'))
    assert.equal(
      ornament.querySelector(
        'a, button, input, select, textarea, iframe, [contenteditable], [tabindex], audio[controls], video[controls]'
      ),
      null
    )
    assertCompleteFallback(ornament)

    await act(async () => rendered.root.unmount())
  })

  test('isolates one subdued metal-fx instance behind the runtime seam', () => {
    const host = readFileSync(
      new URL('./forge-metal-window-ornament.tsx', import.meta.url),
      'utf8'
    )
    const policy = readFileSync(
      new URL('./forge-metal-window-ornament-policy.ts', import.meta.url),
      'utf8'
    )
    const runtime = readFileSync(
      new URL('./forge-metal-window-ornament-runtime.tsx', import.meta.url),
      'utf8'
    )
    const styles = readFileSync(
      new URL('./forge-metal-window-ornament.module.css', import.meta.url),
      'utf8'
    )

    assert.equal(host.includes("from 'metal-fx'"), false)
    assert.equal(policy.includes("from 'metal-fx'"), false)
    assert.equal(styles.includes('metal-fx'), false)
    assert.equal(runtime.includes("from 'metal-fx'"), true)
    assert.equal((runtime.match(/<MetalFx\b/gu) ?? []).length, 1)
    assert.equal(runtime.includes("preset='silver'"), true)
    assert.equal(runtime.includes("theme='light'"), true)
    assert.equal(runtime.includes('normalizeHostStyles={false}'), true)
    assert.equal(runtime.includes('disableGlow'), true)
    assert.equal(runtime.includes('reflectionTargets'), false)
    assert.equal(runtime.includes('trackPointer'), false)
    assert.equal(runtime.includes('pointerTracking'), false)
    assert.equal(runtime.includes('controls'), false)
    assert.equal(runtime.includes("theme='auto'"), false)
    assert.equal(runtime.includes('setSharedPreset'), false)
    assert.equal(runtime.includes('pauseShared'), false)
    assert.equal(runtime.includes('resumeShared'), false)
    assert.equal(runtime.includes('PRESETS'), false)
    assert.equal(styles.includes('pointer-events: none'), true)
    assert.equal(styles.includes('width: 3.25rem'), true)
    assert.equal(styles.includes('var(--forge-'), true)
    assert.equal(styles.includes('@keyframes'), false)
  })
})
