/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const dom = new Window({ url: 'http://localhost/' })
for (const name of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Element',
  'Node',
  'Event',
  'MouseEvent',
  'KeyboardEvent',
  'MutationObserver',
  'getComputedStyle',
  'requestAnimationFrame',
  'cancelAnimationFrame',
] as const) {
  Object.defineProperty(globalThis, name, {
    configurable: true,
    value: dom[name],
  })
}
Object.defineProperty(dom.HTMLCanvasElement.prototype, 'getContext', {
  configurable: true,
  value: () => null,
})
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { DrawingSnakeGame } = await import('./drawing-snake-game')
const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
after(() => dom.close())

test('snake controls capture arrows only inside the focused game', async () => {
  const host = document.createElement('div')
  const outside = document.createElement('button')
  document.body.append(outside, host)
  const root = createRoot(host)
  try {
    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <DrawingSnakeGame />
        </I18nextProvider>
      )
    })
    const outerEvent = new window.KeyboardEvent('keydown', {
      key: 'ArrowDown',
      bubbles: true,
      cancelable: true,
    })
    outside.focus()
    outside.dispatchEvent(outerEvent)
    assert.equal(outerEvent.defaultPrevented, false)
    const canvas = host.querySelector('canvas')
    assert.ok(canvas)
    assert.equal(canvas.tabIndex, 0)
    const innerEvent = new window.KeyboardEvent('keydown', {
      key: 'ArrowLeft',
      bubbles: true,
      cancelable: true,
    })
    canvas.focus()
    canvas.dispatchEvent(innerEvent)
    assert.equal(innerEvent.defaultPrevented, true)
    const shortcut = new window.KeyboardEvent('keydown', {
      key: 'ArrowLeft',
      ctrlKey: true,
      bubbles: true,
      cancelable: true,
    })
    canvas.dispatchEvent(shortcut)
    assert.equal(shortcut.defaultPrevented, false)
  } finally {
    await act(async () => root.unmount())
  }
})
