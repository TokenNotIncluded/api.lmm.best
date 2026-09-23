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
import { test } from 'node:test'

import { Window } from 'happy-dom'

import { mountHomeMotion } from './home-motion'

function fixture() {
  const view = new Window({ url: 'https://example.test/' })
  const frames = new Map<number, FrameRequestCallback>()
  let nextFrame = 0
  let disconnected = 0
  let intersection:
    | ((entries: { isIntersecting: boolean }[]) => void)
    | undefined
  const listeners = new Set<() => void>()
  const reduced = {
    matches: false,
    addEventListener(_event: string, listener: () => void) {
      listeners.add(listener)
    },
    removeEventListener(_event: string, listener: () => void) {
      listeners.delete(listener)
    },
  }
  Object.defineProperty(view, 'matchMedia', {
    value: (query: string) =>
      query.includes('prefers-reduced-motion')
        ? reduced
        : { matches: true, addEventListener() {}, removeEventListener() {} },
  })
  Object.defineProperty(view.document, 'hidden', {
    configurable: true,
    value: false,
  })
  Object.defineProperty(view, 'IntersectionObserver', {
    configurable: true,
    value: class {
      constructor(callback: typeof intersection) {
        intersection = callback
      }
      observe() {}
      disconnect() {
        disconnected += 1
      }
    },
  })
  Object.defineProperty(view, 'ResizeObserver', {
    value: class {
      observe() {}
      disconnect() {
        disconnected += 1
      }
    },
  })
  const globals = {
    window: view,
    document: view.document,
    navigator: view.navigator,
    requestAnimationFrame: (callback: FrameRequestCallback) => {
      frames.set(++nextFrame, callback)
      return nextFrame
    },
    cancelAnimationFrame: (id: number) => {
      frames.delete(id)
    },
  }
  const previous = new Map<string, PropertyDescriptor | undefined>()
  for (const [key, value] of Object.entries(globals)) {
    previous.set(key, Object.getOwnPropertyDescriptor(globalThis, key))
    Object.defineProperty(globalThis, key, { configurable: true, value })
  }
  document.body.innerHTML = `<main>
    <section data-cinema><div data-cinema-inner><div data-token-cloud aria-hidden="true"></div><canvas data-film></canvas><input data-home-token-field value="api" />
      <section data-cinema-panel="0"></section><section data-cinema-panel="1"></section>
      <ol><li data-cinema-step><button data-cinema-jump="0">Home</button></li><li data-cinema-step><button data-cinema-jump="1">API</button></li></ol>
    </div></section>
    <button data-motion-toggle><span data-play-label>Play</span><span data-pause-label>Pause</span></button>
    <section data-story>
      <div data-story-step></div><div data-story-step></div><div data-story-step></div>
      <div data-story-panel><button>One</button></div>
      <div data-story-panel><button>Two</button></div>
      <div data-story-panel><button>Three</button></div>
    </section></main>`
  const root = document.querySelector<HTMLElement>('main')
  assert.ok(root)
  const canvas = root.querySelector('canvas')
  assert.ok(canvas)
  // A zero-size canvas never draws geometry, but retains a real animation
  // lifecycle. The clock below is advanced explicitly, not by real timers.
  Object.defineProperty(canvas, 'getContext', { value: () => ({}) })
  let cleanup: (() => void) | undefined
  return {
    root,
    view,
    frames,
    reduced,
    get disconnected() {
      return disconnected
    },
    mount() {
      cleanup = mountHomeMotion(root)
    },
    visible(value: boolean) {
      intersection?.([{ isIntersecting: value }])
    },
    reduce(value: boolean) {
      reduced.matches = value
      for (const listener of listeners) listener()
    },
    tick(now = 100) {
      const pending = [...frames.values()]
      frames.clear()
      for (const callback of pending) callback(now)
    },
    stop() {
      cleanup?.()
      cleanup = undefined
    },
    close() {
      cleanup?.()
      view.close()
      for (const [key, descriptor] of previous) {
        if (descriptor) Object.defineProperty(globalThis, key, descriptor)
        else Reflect.deleteProperty(globalThis, key)
      }
    },
  }
}

test('motion uses the feature-tested window observers and releases all owned work', () => {
  const page = fixture()
  try {
    page.mount()
    assert.ok(page.root.querySelectorAll('[data-token-particle]').length > 0)
    page.visible(true)
    page.tick()
    assert.equal(page.root.dataset.motion, 'playing')
    assert.equal(
      page.frames.size,
      0,
      'idle artwork does not run an animation loop'
    )
    page.stop()
    assert.equal(page.root.querySelectorAll('[data-token-particle]').length, 0)
    assert.equal(page.disconnected, 2)
    assert.equal(page.frames.size, 0)
    assert.equal(page.root.dataset.motion, undefined)
    page.reduce(true)
    assert.equal(page.frames.size, 0)
  } finally {
    page.close()
  }
})

test('typing causes one bounded response and stops once the result is visible', () => {
  const page = fixture()
  try {
    page.mount()
    page.visible(true)
    page.tick()
    const input = page.root.querySelector<HTMLInputElement>(
      '[data-home-token-field]'
    )
    assert.ok(input)
    input.value = 'gpt'
    input.dispatchEvent(new page.view.Event('input') as unknown as Event)
    page.tick(200)
    assert.equal(page.frames.size, 1)
    for (let now = 300; now <= 1200; now += 100) page.tick(now)
    assert.equal(page.frames.size, 0, 'a completed response stays still')
    page.reduce(true)
    page.tick(1300)
    input.value = 'api'
    input.dispatchEvent(new page.view.Event('input') as unknown as Event)
    page.tick(1400)
    assert.equal(
      page.frames.size,
      0,
      'reduced motion updates the result without replaying'
    )
  } finally {
    page.close()
  }
})

test('scene controls open a chapter without scrolling and preserve keyboard access', () => {
  const page = fixture()
  try {
    page.mount()
    page.tick()
    const button = page.root.querySelector<HTMLButtonElement>(
      '[data-cinema-jump="1"]'
    )
    assert.ok(button)
    button.click()
    page.tick(200)
    assert.equal(button.getAttribute('aria-pressed'), 'true')
    assert.equal(
      page.root.querySelector<HTMLElement>('[data-cinema-panel="1"]')?.inert,
      false
    )
    assert.equal(
      page.root.querySelector<HTMLElement>('[data-cinema-panel="0"]')?.inert,
      true
    )
  } finally {
    page.close()
  }
})

test('reduced motion stops the film and leaves the useful final panel accessible', () => {
  const page = fixture()
  try {
    page.mount()
    page.visible(true)
    page.tick()
    page.reduce(true)
    page.tick(200)
    assert.equal(page.frames.size, 0)
    assert.equal(page.root.dataset.motion, 'reduced')
    assert.equal(
      page.root.querySelector<HTMLElement>('[data-motion-toggle]')?.hidden,
      true
    )
    assert.equal(
      page.root
        .querySelector<HTMLElement>('[data-cinema-inner]')
        ?.style.getPropertyValue('--scene-progress'),
      '0'
    )
    const panels = [
      ...page.root.querySelectorAll<HTMLElement>('[data-story-panel]'),
    ]
    assert.deepEqual(
      panels.map((panel) => panel.inert),
      [true, true, false]
    )
    assert.equal(panels[2].getAttribute('aria-hidden'), 'false')
  } finally {
    page.close()
  }
})

test('pausing and hidden tabs do not retain a running animation loop', () => {
  const page = fixture()
  try {
    page.mount()
    page.visible(true)
    page.tick()
    const toggle = page.root.querySelector<HTMLButtonElement>(
      '[data-motion-toggle]'
    )
    assert.ok(toggle)
    toggle.click()
    page.tick(200)
    assert.equal(page.root.dataset.motion, 'paused')
    assert.equal(toggle.getAttribute('aria-pressed'), 'true')
    assert.equal(page.frames.size, 0)
    toggle.click()
    assert.equal(page.frames.size, 1)
    Object.defineProperty(document, 'hidden', {
      configurable: true,
      value: true,
    })
    page.view.document.dispatchEvent(new page.view.Event('visibilitychange'))
    assert.equal(page.frames.size, 0)
  } finally {
    page.close()
  }
})

test('missing observer support leaves static content usable without attaching an animation', () => {
  const page = fixture()
  try {
    Object.defineProperty(page.view, 'IntersectionObserver', {
      value: undefined,
    })
    page.mount()
    assert.equal(page.frames.size, 0)
    assert.equal(
      page.root.querySelector<HTMLElement>('[data-motion-toggle]')?.hidden,
      true
    )
    assert.equal(page.root.querySelectorAll('[data-story-panel]').length, 3)
  } finally {
    page.close()
  }
})

test('losing the graphics context stops drawing and exposes a usable static page', () => {
  const page = fixture()
  try {
    page.mount()
    page.visible(true)
    page.tick()
    const canvas = page.root.querySelector('canvas')
    assert.ok(canvas)
    canvas.dispatchEvent(
      new page.view.Event('webglcontextlost') as unknown as Event
    )
    page.tick(200)
    assert.equal(page.frames.size, 0)
    assert.equal(page.root.dataset.motion, 'static')
    assert.equal(
      page.root.querySelector<HTMLElement>('[data-motion-toggle]')?.hidden,
      true
    )
  } finally {
    page.close()
  }
})
