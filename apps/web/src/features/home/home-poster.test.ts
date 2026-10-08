/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createHomePoster } from './home-poster'

const pointer = {
  x: 32,
  y: 32,
  vx: 0,
  vy: 0,
  previousX: 32,
  previousY: 32,
  active: false,
}

function fixture() {
  const root = { className: 'dark' }
  let ground = '#070707'
  const previous = new Map<string, PropertyDescriptor | undefined>()
  const globals = {
    window: { devicePixelRatio: 1 },
    document: {
      documentElement: root,
      createElement() {
        const image = {
          onerror: null as (() => void) | null,
          set src(_value: string) {
            queueMicrotask(() => image.onerror?.())
          },
        }
        return image
      },
    },
    getComputedStyle: () => ({ getPropertyValue: () => ground }),
  }
  for (const [key, value] of Object.entries(globals)) {
    previous.set(key, Object.getOwnPropertyDescriptor(globalThis, key))
    Object.defineProperty(globalThis, key, { configurable: true, value })
  }
  const posters: NonNullable<ReturnType<typeof createHomePoster>>[] = []
  return {
    canvas() {
      let pixels = new Uint8ClampedArray()
      const element = {
        clientWidth: 64,
        clientHeight: 64,
        width: 0,
        height: 0,
        dataset: {} as Record<string, string>,
        getContext: () => ({
          createImageData: (width: number, height: number) => ({
            width,
            height,
            data: new Uint8ClampedArray(width * height * 4),
          }),
          putImageData: (image: ImageData) => {
            pixels = new Uint8ClampedArray(image.data)
          },
        }),
      }
      const poster = createHomePoster(element as unknown as HTMLCanvasElement)
      assert.ok(poster)
      posters.push(poster)
      return { element, poster, pixels: () => pixels }
    },
    light() {
      root.className = 'light'
      ground = '#f6f4f0'
    },
    close() {
      for (const poster of posters) poster.dispose()
      for (const [key, descriptor] of previous) {
        if (descriptor) Object.defineProperty(globalThis, key, descriptor)
        else Reflect.deleteProperty(globalThis, key)
      }
    },
  }
}

test('failed image loading still produces the lotus and future is not a lotus', async () => {
  const view = fixture()
  try {
    const { element, poster, pixels } = view.canvas()
    await Promise.resolve()
    poster.draw(0, pointer, 0, true)
    assert.equal(element.dataset.ready, 'true')
    assert.equal(element.dataset.sculpture, 'lotus')
    assert.ok(pixels().some((n, i) => i % 4 !== 3 && n !== 7))
    poster.draw(4, pointer, 0, true)
    assert.equal(element.dataset.sculpture, 'pelicanBicycle')
  } finally {
    view.close()
  }
})

test('page and mobile canvas clocks are independent and survive pause', () => {
  const view = fixture()
  try {
    const first = view.canvas(),
      second = view.canvas()
    for (let i = 0; i < 122; i++) first.poster.draw(0, pointer, 0.08)
    assert.equal(first.element.dataset.sculpture, 'fish')
    second.poster.draw(0, pointer, 0, true)
    assert.equal(second.element.dataset.sculpture, 'lotus')
    for (let i = 0; i < 10; i++) first.poster.draw(0, pointer, 10, true)
    assert.equal(first.element.dataset.sculpture, 'fish')
    first.poster.draw(4, pointer, 0.08)
    assert.equal(first.element.dataset.sculpture, 'pelicanBicycle')
    first.poster.draw(0, pointer, 0, true)
    assert.equal(first.element.dataset.sculpture, 'fish')
  } finally {
    view.close()
  }
})

test('pause freezes brush offsets, theme redraws use light pixels, disposal is final', () => {
  const view = fixture()
  try {
    const { element, poster, pixels } = view.canvas()
    const brush = { ...pointer, active: true, vx: 500 }
    for (let i = 0; i < 5; i++) poster.draw(2, brush, 1 / 30)
    poster.draw(2, brush, 0, true)
    const frozen = pixels()
    poster.draw(2, pointer, 0.06, true)
    assert.deepEqual(pixels(), frozen)
    view.light()
    poster.draw(2, pointer, 0, true)
    assert.deepEqual(Array.from(pixels().slice(0, 4)), [246, 244, 240, 255])
    poster.dispose()
    assert.equal(element.width, 0)
    assert.equal(element.height, 0)
    assert.equal(element.dataset.ready, undefined)
    const disposed = pixels()
    poster.draw(1, brush, 0.08)
    assert.deepEqual(pixels(), disposed)
  } finally {
    view.close()
  }
})
