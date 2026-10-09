/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

import { observeMobileScroll } from './mobile-scroll-controller'

const dom = new Window({ url: 'https://console.example.test/' })
for (const key of [
  'window',
  'document',
  'HTMLElement',
  'Node',
  'Event',
] as const) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: dom[key],
  })
}
const media = new dom.EventTarget() as unknown as MediaQueryList
Object.defineProperty(media, 'matches', {
  configurable: true,
  writable: true,
  value: true,
})
Object.defineProperty(window, 'matchMedia', {
  configurable: true,
  value: () => media,
})
after(() => dom.close())

function setMobile(matches: boolean) {
  Object.defineProperty(media, 'matches', {
    configurable: true,
    value: matches,
  })
  media.dispatchEvent(new Event('change'))
}

function fixture(documentScroll = false) {
  setMobile(true)
  const root = document.createElement('div')
  root.innerHTML =
    '<div data-mobile-scroll-chrome data-mode="flow"><button>Menu</button></div><main><div data-list></div></main>'
  document.body.append(root)
  const scroll = root.querySelector<HTMLElement>('[data-list]')!
  Object.defineProperties(scroll, {
    clientHeight: { configurable: true, value: 500 },
    scrollHeight: { configurable: true, value: 2500 },
  })
  const states: boolean[] = []
  const dispose = observeMobileScroll(
    root,
    (hidden) => states.push(hidden),
    documentScroll
  )
  const move = (top: number, target = scroll) => {
    target.scrollTop = top
    target.dispatchEvent(new Event('scroll'))
  }
  return {
    root,
    scroll,
    states,
    move,
    dispose,
    close() {
      dispose()
      document.body.replaceChildren()
    },
  }
}

function check(run: (app: ReturnType<typeof fixture>) => void) {
  const app = fixture()
  try {
    run(app)
  } finally {
    app.close()
  }
}

test('capture observes a non-bubbling nested list scroll and reveals on reversal', () => {
  check(({ move, states }) => {
    move(0)
    move(40)
    move(80)
    move(65)
    assert.deepEqual(states, [false, true, false])
  })
})

test('desktop keeps controls visible and changing breakpoint restores them', () => {
  check(({ move, states }) => {
    setMobile(false)
    move(0)
    move(80)
    assert.deepEqual(states, [false])
    setMobile(true)
    move(100)
    move(140)
    setMobile(false)
    assert.deepEqual(states, [false, true, false])
  })
})

test('text editing restores controls and keeps them available', () => {
  check(({ root, move, states }) => {
    move(0)
    move(80)
    const input = document.createElement('input')
    root.append(input)
    input.focus()
    move(120)
    move(180)
    assert.deepEqual(states, [false, true, false])
  })
})

test('open header menus do not disappear under a continuing scroll', () => {
  check(({ root, move, states }) => {
    root.querySelector('button')!.setAttribute('aria-expanded', 'true')
    move(0)
    move(100)
    assert.deepEqual(states, [false])
  })
})

test('dialogs, text areas, and horizontal-only widgets do not drive page chrome', () => {
  check(({ root, move, states }) => {
    move(0)
    move(80)
    for (const markup of [
      '<div role="dialog"><div data-inner></div></div>',
      '<textarea data-inner></textarea>',
      '<div data-inner data-mobile-scroll-ignore></div>',
    ]) {
      const host = document.createElement('div')
      host.innerHTML = markup
      root.querySelector('main')!.append(host)
      const inner = host.querySelector<HTMLElement>('[data-inner]')!
      Object.defineProperties(inner, {
        scrollHeight: { value: 2000 },
        clientHeight: { value: 500 },
      })
      move(0, inner)
      move(100, inner)
    }
    const horizontal = document.createElement('div')
    root.querySelector('main')!.append(horizontal)
    move(0, horizontal)
    assert.deepEqual(states, [false, true])
  })
})

test('modal dialogs pin the background controls without listening inside the dialog', () => {
  check(({ move, states }) => {
    const dialog = document.createElement('div')
    dialog.setAttribute('role', 'dialog')
    dialog.setAttribute('aria-modal', 'true')
    document.body.append(dialog)
    move(0)
    move(100)
    assert.deepEqual(states, [false])
  })
})

test('short content keeps enough range to restore the controls', () => {
  check(({ root, scroll, move, states }) => {
    Object.defineProperty(scroll, 'scrollHeight', { value: 600 })
    const chrome = root.querySelector<HTMLElement>(
      '[data-mobile-scroll-chrome]'
    )!
    chrome.getBoundingClientRect = () => ({ height: 80 }) as DOMRect
    move(0)
    move(80)
    assert.deepEqual(states, [false])
  })
})

test('keyboard navigation reveals hidden controls and cleanup removes listeners', () => {
  check(({ move, states, dispose }) => {
    move(0)
    move(80)
    document.dispatchEvent(
      new dom.KeyboardEvent('keydown', {
        key: 'Tab',
      }) as unknown as KeyboardEvent
    )
    assert.deepEqual(states, [false, true, false])
    dispose()
    move(100)
    move(180)
    assert.deepEqual(states, [false, true, false])
  })
})

test('public document scrolling uses the same direction thresholds', () => {
  const app = fixture(true)
  try {
    const scroll = document.scrollingElement || document.documentElement
    Object.defineProperties(scroll, {
      clientHeight: { configurable: true, value: 500 },
      scrollHeight: { configurable: true, value: 2500 },
    })
    for (const top of [0, 100, 200, 180]) {
      scroll.scrollTop = top
      document.dispatchEvent(new Event('scroll'))
    }
    assert.deepEqual(app.states, [false, true, false])
  } finally {
    app.close()
  }
})

test('pointer input seeds a new scroll region before its first movement', () => {
  check(({ scroll, move, states }) => {
    scroll.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    move(80)
    assert.deepEqual(states, [false, true])
  })
})

test('a wheel event delivered after compositor scrolling preserves upward travel', () => {
  check(({ scroll, move, states }) => {
    move(0)
    move(100)
    Object.defineProperty(scroll, 'clientHeight', { value: 800 })
    scroll.scrollTop = 80
    scroll.dispatchEvent(new Event('wheel', { bubbles: true }))
    scroll.dispatchEvent(new Event('scroll'))
    assert.deepEqual(states, [false, true, false])
  })
})
