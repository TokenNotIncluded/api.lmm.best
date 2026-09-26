/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({ url: 'https://example.test/red-packets' })
domWindow.document.write('<!doctype html><html><body></body></html>')
Object.defineProperty(domWindow.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'matchMedia',
  'customElements',
  'CSSStyleSheet',
  'localStorage',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')

const { WalletTokenCloud } = await import('./wallet-token-cloud')
const { walletCloudParticleCount } = await import('../lib/wallet-token-cloud')
const observers = new Set<IntersectionObserverCallback>()
Object.defineProperty(globalThis, 'IntersectionObserver', {
  configurable: true,
  value: class {
    constructor(private callback: IntersectionObserverCallback) {}
    observe() {
      observers.add(this.callback)
    }
    disconnect() {
      observers.delete(this.callback)
    }
  },
})
const timers = new Map<number, () => void>()
let timerId = 900_000
const originalTimeout = window.setTimeout.bind(window)
const originalClear = window.clearTimeout.bind(window)
window.setTimeout = ((handler: TimerHandler, ms?: number) => {
  if (ms === 3000 && typeof handler === 'function') {
    const id = ++timerId
    timers.set(id, handler as () => void)
    return id
  }
  return originalTimeout(handler, ms)
}) as typeof window.setTimeout
window.clearTimeout = ((id: number) => {
  timers.delete(id)
  originalClear(id)
}) as typeof window.clearTimeout
let root: ReturnType<typeof createRoot> | null = null
let host: HTMLDivElement | null = null
afterEach(async () => {
  await act(async () => root?.unmount())
  host?.remove()
  timers.clear()
  root = null
})
after(() => domWindow.close())
async function visible(value: boolean) {
  await act(async () => {
    for (const callback of observers) {
      callback(
        [
          {
            isIntersecting: value,
            intersectionRatio: value ? 1 : 0,
          } as IntersectionObserverEntry,
        ],
        {} as IntersectionObserver
      )
    }
  })
}
async function mount(amount = 120) {
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
  const completed: number[] = []
  const node = (
    <WalletTokenCloud
      amount={amount}
      success={{ orderId: 7, beforeCredits: 100, creditedCredits: 100 }}
      onSuccessComplete={(id) => completed.push(id)}
    />
  )
  await act(async () => root!.render(node))
  return {
    cloud: host.querySelector('[data-testid="wallet-token-cloud-balance"]')!,
    completed,
    node,
  }
}

test('off-screen success is retained and starts only when visible', async () => {
  const { cloud, completed } = await mount()
  assert.equal(cloud.getAttribute('data-success'), null)
  assert.equal(timers.size, 0)
  await visible(true)
  assert.equal(cloud.getAttribute('data-success'), 'true')
  assert.equal(timers.size, 1)
  await visible(false)
  assert.equal(
    timers.size,
    0,
    'leaving the viewport cannot consume an unseen completion'
  )
  assert.deepEqual(completed, [])
  await visible(true)
  await act(async () => {
    for (const finish of timers.values()) finish()
    timers.clear()
  })
  assert.equal(cloud.getAttribute('data-success'), null)
  assert.deepEqual(completed, [7])
  await visible(false)
  await visible(true)
  assert.equal(timers.size, 0, 'one order is not replayed after completion')
})

test('the cloud settles at actual balance even when consumption happens during payment', async () => {
  const { cloud } = await mount(120)
  await visible(true)
  const expected = walletCloudParticleCount(120, 'balance')
  assert.equal(
    cloud.querySelectorAll(
      '.wallet-token-cloud-particle,.wallet-token-cloud-added'
    ).length,
    expected
  )
  await act(async () => {
    for (const finish of timers.values()) finish()
    timers.clear()
  })
  assert.equal(
    cloud.querySelectorAll('.wallet-token-cloud-particle').length,
    expected
  )
})
