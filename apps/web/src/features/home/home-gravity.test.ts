/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { Window } from 'happy-dom'

import { insideInput, mountHomeGravity } from './home-gravity'

test('the title drop area includes its edges', () => {
  const rect = { left: 10, right: 90, top: 20, bottom: 80 } as DOMRect
  assert.equal(insideInput(10, 20, rect), true)
  assert.equal(insideInput(90, 80, rect), true)
  assert.equal(insideInput(9, 20, rect), false)
})

test('each chapter rebuilds its own letters and a missed drag restores them', async () => {
  const view = new Window({ url: 'https://example.test/' })
  const previousWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: view,
  })
  try {
    Object.defineProperty(view, 'matchMedia', {
      value: () => ({ matches: false }),
    })
    view.document.body.innerHTML = `
      <main><div data-cinema-inner></div><div data-token-input></div>
        <section data-cinema-panel><h2 data-gravity-title>One</h2>
          <p data-gravity-description><span data-gravity-glyph>A</span><span data-gravity-glyph>B</span></p>
        </section>
        <section data-cinema-panel><h2 data-gravity-title>Two</h2>
          <p data-gravity-description><span data-gravity-glyph>C</span></p>
        </section>
      </main>`
    const root = view.document.querySelector('main') as unknown as HTMLElement
    const input = root.querySelector<HTMLElement>('[data-token-input]')!
    Object.defineProperty(input, 'getBoundingClientRect', {
      value: () => ({
        left: 0,
        right: 100,
        top: 0,
        bottom: 100,
        width: 100,
        height: 100,
      }),
    })
    const calls: string[] = []
    let cancelled = 0
    for (const glyph of root.querySelectorAll<HTMLElement>(
      '[data-gravity-glyph]'
    )) {
      Object.defineProperty(glyph, 'animate', {
        value: () => {
          calls.push(glyph.textContent || '')
          return {
            finished: Promise.resolve(),
            cancel: () => {
              cancelled++
            },
          }
        },
      })
    }
    const stop = mountHomeGravity(root, input)
    const [first, second] = root.querySelectorAll<HTMLElement>(
      '[data-gravity-title]'
    )
    first.dispatchEvent(
      new view.KeyboardEvent('keydown', {
        key: 'Enter',
        bubbles: true,
      }) as unknown as Event
    )
    await new Promise((resolve) => setTimeout(resolve, 0))
    assert.deepEqual(calls, ['A', 'B', 'A', 'B'])
    second.dispatchEvent(
      new view.KeyboardEvent('keydown', {
        key: 'Enter',
        bubbles: true,
      }) as unknown as Event
    )
    await new Promise((resolve) => setTimeout(resolve, 0))
    assert.deepEqual(calls.slice(4), ['C', 'C'])
    const pointer = (type: string, x: number, y: number) => {
      const event = new view.Event(type, { bubbles: true })
      Object.assign(event, {
        pointerType: 'mouse',
        pointerId: 1,
        button: 0,
        clientX: x,
        clientY: y,
      })
      first.dispatchEvent(event as unknown as Event)
    }
    pointer('pointerdown', 200, 200)
    pointer('pointermove', 220, 220)
    pointer('pointerup', 220, 220)
    assert.equal(first.hasAttribute('data-dragging'), false)
    assert.equal(first.style.transform, '')
    assert.ok(cancelled > 0)
    stop()
  } finally {
    view.close()
    if (previousWindow) {
      Object.defineProperty(globalThis, 'window', previousWindow)
    } else {
      Reflect.deleteProperty(globalThis, 'window')
    }
  }
})
