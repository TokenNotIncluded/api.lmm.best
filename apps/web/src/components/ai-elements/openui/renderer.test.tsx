/* Copyright (C) 2026 LIghtJUNction; licensed under AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, describe, it } from 'node:test'

import { Window } from 'happy-dom'
import { act } from 'react'

import { OpenUIBlock } from './block'
import OpenUIRenderer from './renderer'
import { ResponseStreamingContext } from './streaming-context'

const browser = new Window({ url: 'https://example.test' })
const globals = { window: browser, document: browser.document, navigator: browser.navigator, HTMLElement: browser.HTMLElement, IS_REACT_ACT_ENVIRONMENT: true }
const originals = new Map<string, PropertyDescriptor | undefined>()
for (const [key, value] of Object.entries(globals)) {
  originals.set(key, Object.getOwnPropertyDescriptor(globalThis, key))
  Object.defineProperty(globalThis, key, { configurable: true, writable: true, value })
}
const { createRoot } = await import('react-dom/client')
let host: HTMLDivElement
let root: ReturnType<typeof createRoot>
const valid = 'root = Stack([Metric("Requests", "12", "Synthetic fixture"), ConsoleLink("usage", "View usage")])'

beforeEach(() => {
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})
afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
})
after(async () => {
  await browser.happyDOM.abort()
  for (const [key, descriptor] of originals) {
    if (descriptor) {
      Object.defineProperty(globalThis, key, descriptor)
    } else {
      Reflect.deleteProperty(globalThis, key)
    }
  }
})

async function render(code: string, isStreaming = false) {
  await act(async () => {
    root.render(<OpenUIRenderer code={code} isStreaming={isStreaming} fallback={<p>Fallback</p>} />)
  })
}

describe('real OpenUI renderer', () => {
  it('renders approved components and fixed internal navigation', async () => {
    await render(valid)
    assert.ok(host.textContent?.includes('Synthetic fixture'))
    assert.ok(host.textContent?.includes('12'))
    assert.equal(host.querySelector('a')?.getAttribute('href'), '/usage-logs')
  })
  it('falls back on unsupported output and recovers on a new response', async () => {
    await render('root = UnknownComponent("bad")')
    assert.ok(host.textContent?.includes('Fallback'))
    await render(valid)
    assert.ok(host.textContent?.includes('Synthetic fixture'))
    assert.ok(!host.textContent?.includes('Fallback'))
  })
  it('revalidates an incomplete stream when the response finishes', async () => {
    const invalid = 'root = UnknownComponent("bad")'
    await render(invalid, true)
    await render(invalid, false)
    assert.ok(host.textContent?.includes('Fallback'))
  })
  it('sorts rows with a user click without changing the source response', async () => {
    await render('root = Stack([DataTable("Requests", ["Model", "Count"], [["Model A", 12], ["Model B", 2]])])')
    const button = host.querySelectorAll('button')[1]
    assert.ok(button)
    await act(async () => button.click())
    assert.ok(host.querySelector('tbody tr')?.textContent?.includes('Model B'))
    assert.equal(host.querySelectorAll('th')[1]?.getAttribute('aria-sort'), 'ascending')
    await act(async () => button.click())
    assert.ok(host.querySelector('tbody tr')?.textContent?.includes('Model A'))
  })
  it('disables block interaction during streaming and enables it on completion', async () => {
    for (const streaming of [true, false]) {
      await act(async () => {
        root.render(<ResponseStreamingContext.Provider value={streaming}><OpenUIBlock code={valid} fallback={<p>Fallback</p>} /></ResponseStreamingContext.Provider>)
      })
      assert.equal(host.querySelector('[data-openui-view]')?.hasAttribute('inert'), streaming)
    }
  })
  it('never renders more than twelve root components', async () => {
    const metrics = Array.from({ length: 14 }, (_, i) => `Metric("Metric ${i}", "1", "Synthetic fixture")`)
    await render(`root = Stack([${metrics.join(',')}])`)
    assert.ok(host.querySelectorAll('dl').length <= 12)
  })
})
