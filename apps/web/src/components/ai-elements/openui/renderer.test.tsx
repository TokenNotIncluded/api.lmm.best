/* Copyright (C) 2026 LIghtJUNction; licensed under AGPL-3.0-or-later. */
import { afterAll, afterEach, beforeEach, describe, expect, it } from 'bun:test'

import { Window } from 'happy-dom'
import { act } from 'react'

import OpenUIRenderer from './renderer'
const browser = new Window({ url: 'https://example.test' })
const globals = {
  window: browser,
  document: browser.document,
  navigator: browser.navigator,
  HTMLElement: browser.HTMLElement,
  IS_REACT_ACT_ENVIRONMENT: true,
}
const originals = new Map<string, PropertyDescriptor | undefined>()
for (const [key, value] of Object.entries(globals)) {
  originals.set(key, Object.getOwnPropertyDescriptor(globalThis, key))
  Object.defineProperty(globalThis, key, {
    configurable: true,
    writable: true,
    value,
  })
}
const { createRoot } = await import('react-dom/client')
let host: HTMLDivElement
let root: ReturnType<typeof createRoot>
const valid =
  'root = Stack([Metric("Requests", "12", "Synthetic fixture"), ConsoleLink("usage", "View usage")])'
beforeEach(() => {
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})
afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
})
afterAll(() => {
  browser.happyDOM.abort()
  for (const [key, descriptor] of originals) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor)
    else Reflect.deleteProperty(globalThis, key)
  }
})
async function render(code: string, isStreaming = false) {
  await act(async () => {
    root.render(
      <OpenUIRenderer
        code={code}
        isStreaming={isStreaming}
        fallback={<p>Fallback</p>}
      />
    )
  })
}
describe('real OpenUI renderer', () => {
  it('renders approved components and fixed internal navigation', async () => {
    await render(valid)
    expect(host.textContent).toContain('Synthetic fixture')
    expect(host.textContent).toContain('12')
    expect(host.querySelector('a')?.getAttribute('href')).toBe('/usage-logs')
  })
  it('falls back on unsupported output and recovers on a new response', async () => {
    await render('root = UnknownComponent("bad")')
    expect(host.textContent).toContain('Fallback')
    await render(valid)
    expect(host.textContent).toContain('Synthetic fixture')
    expect(host.textContent).not.toContain('Fallback')
  })
  it('revalidates an incomplete stream when the response finishes', async () => {
    const invalid = 'root = UnknownComponent("bad")'
    await render(invalid, true)
    await render(invalid, false)
    expect(host.textContent).toContain('Fallback')
  })
  it('sorts rows with a user click without changing the source response', async () => {
    await render(
      'root = Stack([DataTable("Requests", ["Model", "Count"], [["Model A", 12], ["Model B", 2]])])'
    )
    const button = host.querySelectorAll('button')[1]
    expect(button).toBeDefined()
    await act(async () => button!.click())
    expect(host.querySelector('tbody tr')?.textContent).toContain('Model B')
    expect(host.querySelectorAll('th')[1]?.getAttribute('aria-sort')).toBe(
      'ascending'
    )
    await act(async () => button!.click())
    expect(host.querySelector('tbody tr')?.textContent).toContain('Model A')
  })
})
