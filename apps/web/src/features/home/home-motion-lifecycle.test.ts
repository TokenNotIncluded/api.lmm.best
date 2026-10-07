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

function fixture({ graphics = true, observers = true } = {}) {
  const view = new Window({ url: 'https://example.test/' })
  const frames = new Map<number, FrameRequestCallback>()
  const paints = new Map<HTMLCanvasElement, number>()
  const listeners = new Set<() => void>()
  let nextFrame = 0
  let disconnected = 0
  let sectionTop = 64
  let intersection:
    | ((entries: { isIntersecting: boolean }[]) => void)
    | undefined
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
    value: observers
      ? class {
          constructor(callback: typeof intersection) {
            intersection = callback
          }
          observe() {}
          disconnect() {
            disconnected += 1
          }
        }
      : undefined,
  })
  Object.defineProperty(view, 'ResizeObserver', {
    value: observers
      ? class {
          observe() {}
          disconnect() {
            disconnected += 1
          }
        }
      : undefined,
  })
  const globals = {
    window: view,
    document: view.document,
    navigator: view.navigator,
    getComputedStyle: view.getComputedStyle.bind(view),
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
  view.document.body.innerHTML = `<main>
    <section data-cinema><div data-cinema-inner style="top: 64px"><canvas data-film></canvas>
      ${Array.from(
        { length: 5 },
        (_, index) => `<section data-cinema-panel="${index}">
        ${index ? `<canvas data-chapter-film="${index}"></canvas>` : ''}
        <h2>Chapter ${index}</h2><a href="/chapter-${index}">Open ${index}</a></section>`
      ).join('')}
      <ol>${Array.from({ length: 5 }, (_, index) => `<li><button data-cinema-jump="${index}">Chapter ${index}</button></li>`).join('')}</ol>
    </div></section>
    <button data-motion-toggle><span data-play-label>Resume</span><span data-pause-label>Pause</span></button>
    <section data-story>
      <div data-story-step></div><div data-story-step></div><div data-story-step></div>
      <div data-story-panel><button>One</button></div>
      <div data-story-panel><button>Two</button></div>
      <div data-story-panel><button>Three</button></div>
    </section></main>`
  const root = view.document.querySelector<HTMLElement>('main')!
  const cinema = root.querySelector<HTMLElement>('[data-cinema]')!
  const inner = root.querySelector<HTMLElement>('[data-cinema-inner]')!
  const canvases = [...root.querySelectorAll<HTMLCanvasElement>('canvas')]
  Object.defineProperty(cinema, 'getBoundingClientRect', {
    value: () => ({ top: sectionTop, height: 3780, bottom: sectionTop + 3780 }),
  })
  Object.defineProperty(inner, 'getBoundingClientRect', {
    value: () => ({ top: 64, height: 836, bottom: 900 }),
  })
  for (const canvas of canvases) {
    Object.defineProperty(canvas, 'clientWidth', { value: 400 })
    Object.defineProperty(canvas, 'clientHeight', { value: 300 })
    Object.defineProperty(canvas, 'getBoundingClientRect', {
      value: () => ({ top: 80, bottom: 380, left: 0, width: 400, height: 300 }),
    })
    Object.defineProperty(canvas, 'getContext', {
      value: () =>
        graphics
          ? {
              setTransform() {},
              fillRect() {
                paints.set(canvas, (paints.get(canvas) ?? 0) + 1)
              },
              createImageData(width: number, height: number) {
                return {
                  width,
                  height,
                  colorSpace: 'srgb',
                  data: new Uint8ClampedArray(width * height * 4),
                }
              },
              putImageData(image: ImageData, _x: number, _y: number) {
                assert.equal(image.data.length, image.width * image.height * 4)
                paints.set(canvas, (paints.get(canvas) ?? 0) + 1)
              },
            }
          : null,
    })
  }
  let cleanup: (() => void) | undefined
  let now = 0
  const resize = (width: number, height: number) => {
    Object.defineProperty(view, 'innerWidth', {
      configurable: true,
      value: width,
    })
    Object.defineProperty(view, 'innerHeight', {
      configurable: true,
      value: height,
    })
    view.dispatchEvent(new view.Event('resize'))
  }
  resize(1440, 900)
  return {
    root,
    view,
    inner,
    canvases,
    frames,
    paints,
    resize,
    get disconnected() {
      return disconnected
    },
    get preferenceListeners() {
      return listeners.size
    },
    mount() {
      cleanup = mountHomeMotion(root)
    },
    visible(value: boolean) {
      intersection?.([{ isIntersecting: value }])
    },
    hidden(value: boolean) {
      Object.defineProperty(view.document, 'hidden', {
        configurable: true,
        value,
      })
      view.document.dispatchEvent(new view.Event('visibilitychange'))
    },
    reduce(value: boolean) {
      reduced.matches = value
      for (const listener of listeners) listener()
    },
    tick() {
      now += 80
      const pending = [...frames.values()]
      frames.clear()
      for (const callback of pending) callback(now)
    },
    settle() {
      for (let index = 0; index < 22; index++) this.tick()
    },
    scroll(chapter: number) {
      sectionTop = 64 - ((3780 - 836) * chapter) / 4
      view.document.dispatchEvent(new view.Event('scroll'))
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

const panels = (root: HTMLElement) => [
  ...root.querySelectorAll<HTMLElement>('[data-cinema-panel]'),
]

test('the poster paints with one owned loop, suspends offscreen and resumes when visible', () => {
  const page = fixture()
  try {
    page.mount()
    page.tick()
    // While images load, the page retains a static image fallback. This test
    // verifies owned animation and offscreen work; browser review verifies
    // the decoded image's point-sampled artwork.
    assert.ok((page.paints.get(page.canvases[0]) ?? 0) > 0)
    assert.equal(page.root.dataset.motion, 'playing')
    assert.equal(page.frames.size, 1)
    page.visible(false)
    assert.equal(page.frames.size, 0)
    const painted = page.paints.get(page.canvases[0])
    page.resize(1280, 900)
    page.tick()
    assert.equal(page.paints.get(page.canvases[0]), painted)
    page.visible(true)
    page.tick()
    assert.equal(page.frames.size, 1)
    assert.ok(page.paints.get(page.canvases[0])! > painted!)
  } finally {
    page.close()
  }
})

test('hidden documents release their loop and repaint after becoming visible', () => {
  const page = fixture()
  try {
    page.mount()
    page.tick()
    page.hidden(true)
    assert.equal(page.frames.size, 0)
    const painted = page.paints.get(page.canvases[0])
    page.resize(1280, 900)
    page.tick()
    assert.equal(page.paints.get(page.canvases[0]), painted)
    page.hidden(false)
    page.tick()
    assert.equal(page.frames.size, 1)
    assert.ok(page.paints.get(page.canvases[0])! > painted!)
  } finally {
    page.close()
  }
})

test('pause draws a static frame and resume restores a single animation loop', () => {
  const page = fixture()
  try {
    page.mount()
    page.tick()
    const toggle = page.root.querySelector<HTMLButtonElement>(
      '[data-motion-toggle]'
    )!
    toggle.click()
    page.tick()
    assert.equal(page.root.dataset.motion, 'paused')
    assert.equal(toggle.getAttribute('aria-pressed'), 'true')
    assert.equal(page.frames.size, 0)
    const painted = page.paints.get(page.canvases[0])
    page.tick()
    assert.equal(page.paints.get(page.canvases[0]), painted)
    toggle.click()
    page.tick()
    assert.equal(page.root.dataset.motion, 'playing')
    assert.equal(toggle.getAttribute('aria-pressed'), 'false')
    assert.equal(page.frames.size, 1)
  } finally {
    page.close()
  }
})

test('reduced motion stops animation and exposes all chapters and the useful connection panel', () => {
  const page = fixture()
  try {
    page.mount()
    page.tick()
    page.reduce(true)
    page.tick()
    assert.equal(page.root.dataset.motion, 'reduced')
    assert.equal(page.frames.size, 0)
    assert.equal(
      page.root.querySelector<HTMLElement>('[data-motion-toggle]')!.hidden,
      true
    )
    assert.ok(
      panels(page.root).every(
        (panel) => !panel.inert && panel.getAttribute('aria-hidden') === 'false'
      )
    )
    const story = [
      ...page.root.querySelectorAll<HTMLElement>('[data-story-panel]'),
    ]
    assert.deepEqual(
      story.map((panel) => panel.inert),
      [true, true, false]
    )
    assert.ok(
      page.canvases
        .slice(1)
        .every((canvas) => (page.paints.get(canvas) ?? 0) > 0)
    )
    page.reduce(false)
    page.tick()
    assert.equal(page.frames.size, 1)
  } finally {
    page.close()
  }
})

test('all five manual chapters are keyboard accessible and native scrolling takes over', () => {
  const page = fixture()
  try {
    page.mount()
    page.tick()
    for (const chapter of [1, 2, 3, 4, 0]) {
      const button = page.root.querySelector<HTMLButtonElement>(
        `[data-cinema-jump="${chapter}"]`
      )!
      button.focus()
      button.click()
      page.settle()
      assert.equal(button.getAttribute('aria-pressed'), 'true')
      assert.equal(page.inner.dataset.chapter, String(chapter))
      assert.deepEqual(
        panels(page.root).map((panel) => panel.inert),
        Array.from({ length: 5 }, (_, index) => index !== chapter)
      )
      assert.equal(page.view.scrollY, 0)
    }
    for (const chapter of [1, 2, 3, 4, 0]) {
      page.scroll(chapter)
      page.settle()
      assert.equal(page.inner.dataset.chapter, String(chapter))
    }
  } finally {
    page.close()
  }
})

for (const [width, height] of [
  [390, 844],
  [900, 900],
  [844, 390],
]) {
  test(`the ${width} by ${height} reading flow keeps all chapter actions accessible`, () => {
    const page = fixture()
    try {
      page.resize(width, height)
      page.mount()
      page.tick()
      assert.ok(
        panels(page.root).every(
          (panel) =>
            !panel.inert && panel.getAttribute('aria-hidden') === 'false'
        )
      )
      page.resize(1440, 900)
      page.tick()
      assert.deepEqual(
        panels(page.root).map((panel) => panel.inert),
        [false, true, true, true, true]
      )
      page.resize(width, height)
      page.tick()
      assert.ok(panels(page.root).every((panel) => !panel.inert))
    } finally {
      page.close()
    }
  })
}

for (const options of [{ graphics: false }, { observers: false }]) {
  test(`static fallback preserves all five chapters when ${options.graphics === false ? 'canvas' : 'observers'} is unavailable`, () => {
    const page = fixture(options)
    try {
      page.mount()
      page.tick()
      assert.equal(page.root.dataset.motion, 'static')
      assert.equal(page.frames.size, 0)
      assert.equal(
        page.root.querySelector<HTMLElement>('[data-motion-toggle]')!.hidden,
        true
      )
      assert.ok(
        panels(page.root).every(
          (panel) =>
            !panel.inert && panel.getAttribute('aria-hidden') === 'false'
        )
      )
    } finally {
      page.close()
    }
  })
}

test('cleanup releases every canvas, observer, frame and preference listener', () => {
  const page = fixture()
  try {
    page.resize(390, 844)
    page.mount()
    page.tick()
    assert.ok(page.canvases.every((canvas) => canvas.width > 0))
    page.stop()
    assert.equal(page.frames.size, 0)
    assert.equal(page.disconnected, 2)
    assert.equal(page.preferenceListeners, 0)
    assert.equal(page.root.dataset.motion, undefined)
    assert.ok(
      page.canvases.every((canvas) => canvas.width === 0 && canvas.height === 0)
    )
    assert.ok(
      panels(page.root).every(
        (panel) => !panel.inert && !panel.hasAttribute('aria-hidden')
      )
    )
    page.reduce(true)
    page.resize(1440, 900)
    page.hidden(false)
    page.root.querySelector<HTMLButtonElement>('[data-motion-toggle]')!.click()
    page.tick()
    assert.equal(page.frames.size, 0)
  } finally {
    page.close()
  }
})
