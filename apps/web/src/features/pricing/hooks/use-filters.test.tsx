/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
// Bun provides module mocking at runtime; its type declarations are not part of
// this browser-targeted project, so keep the test runner import type-checked.
// @ts-expect-error Bun's test module is available only in the test runtime.
import { mock as moduleMock } from 'bun:test'
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'
import type { Root } from 'react-dom/client'

type SearchState = Record<string, unknown>
type NavigateOptions = {
  search: (previous: SearchState) => SearchState
}

const domWindow = new Window({ url: 'https://console.example.test/pricing' })
domWindow.document.write(
  '<!doctype html><html><head></head><body></body></html>'
)
Object.defineProperty(domWindow.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'history',
  'location',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'SVGElement',
  'customElements',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'PointerEvent',
  'FocusEvent',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'scrollTo',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.defineProperty(globalThis, 'matchMedia', {
  configurable: true,
  value: (media: string) => ({
    matches: false,
    media,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent() {
      return false
    },
  }),
})
Object.defineProperty(globalThis, 'IS_REACT_ACT_ENVIRONMENT', {
  configurable: true,
  value: true,
})

let routeSearch: SearchState = {}
const navigateCalls: SearchState[] = []
type FakeTimerCallback = (...args: never[]) => void
type FakeTimerId = ReturnType<typeof setTimeout>

const realSetTimeout = globalThis.setTimeout
const realClearTimeout = globalThis.clearTimeout
const pendingTimers = new Map<FakeTimerId, FakeTimerCallback>()
let nextTimerId = 0

function installFakeTimers() {
  pendingTimers.clear()
  globalThis.setTimeout = ((callback: FakeTimerCallback) => {
    const id = ++nextTimerId as unknown as FakeTimerId
    pendingTimers.set(id, callback)
    return id
  }) as unknown as typeof setTimeout
  globalThis.clearTimeout = ((id: FakeTimerId) => {
    pendingTimers.delete(id)
  }) as unknown as typeof clearTimeout
}

function restoreRealTimers() {
  globalThis.setTimeout = realSetTimeout
  globalThis.clearTimeout = realClearTimeout
  pendingTimers.clear()
}

function advanceFakeTimers() {
  const timers = [...pendingTimers.values()]
  pendingTimers.clear()
  for (const callback of timers) callback()
}

moduleMock.module('@tanstack/react-router', () => ({
  useSearch: () => routeSearch,
  useNavigate: () => (options: NavigateOptions) => {
    const next = options.search(routeSearch)
    routeSearch = next
    navigateCalls.push(next)
  },
}))

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { useFilters } = await import('./use-filters')

let root: Root | undefined
let container: HTMLDivElement | undefined
let filterResult: ReturnType<typeof useFilters> | undefined

function Harness() {
  filterResult = useFilters([])
  return null
}

beforeEach(async () => {
  routeSearch = {}
  navigateCalls.length = 0
  container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
  await act(async () => {
    root?.render(<Harness />)
  })
})

afterEach(async () => {
  restoreRealTimers()
  if (root) {
    await act(async () => root?.unmount())
  }
  container?.remove()
  root = undefined
  container = undefined
  filterResult = undefined
})

after(() => domWindow.close())

describe('pricing filter URL synchronization', () => {
  test('does not restore a cleared search after the debounce window', async () => {
    assert.ok(filterResult)

    installFakeTimers()
    await act(async () => {
      filterResult?.setSearchInput('stale search')
    })
    await act(async () => {
      filterResult?.clearSearch()
    })
    await act(async () => {
      advanceFakeTimers()
    })

    assert.equal(routeSearch.search, undefined)
    assert.equal(navigateCalls.at(-1)?.search, undefined)
  })

  test('flushes a pending search when another filter changes', async () => {
    assert.ok(filterResult)

    installFakeTimers()
    await act(async () => {
      filterResult?.setSearchInput('needle')
      filterResult?.setVendorFilter('openai')
    })

    assert.equal(routeSearch.search, 'needle')
    assert.equal(routeSearch.vendor, 'openai')
    assert.equal(pendingTimers.size, 0)
    assert.equal(navigateCalls.length, 1)

    await act(async () => {
      advanceFakeTimers()
    })
    assert.equal(navigateCalls.length, 1)
  })
})
