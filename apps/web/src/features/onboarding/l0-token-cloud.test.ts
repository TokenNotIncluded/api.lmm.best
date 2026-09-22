/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  createL0Tokens,
  mountL0TokenCloud,
  projectL0Token,
} from './l0-token-cloud'

function fixture({ reduced = false, contextAvailable = true } = {}) {
  const frames = new Map<number, FrameRequestCallback>()
  let nextFrame = 0
  let paints = 0
  let reads = 0
  let disconnects = 0
  const positions: number[][] = []
  const context = {
    clearRect() {
      paints++
      positions.length = 0
    },
    setTransform() {},
    fillText(_text: string, x: number, y: number) {
      positions.push([x, y])
    },
    fillRect(x: number, y: number) {
      positions.push([x, y])
    },
  }
  const media = Object.assign(new EventTarget(), { matches: reduced })
  const doc = Object.assign(new EventTarget(), { hidden: false })
  const canvas = Object.assign(new EventTarget(), {
    width: 0,
    height: 0,
    getContext: () => (contextAvailable ? context : null),
    getBoundingClientRect: () => {
      reads++
      return { top: 10, bottom: 310, left: 0, width: 760, height: 300 }
    },
  })
  const toggle = Object.assign(new EventTarget(), {
    disabled: false,
    attrs: {} as Record<string, string>,
    setAttribute(key: string, value: string) {
      this.attrs[key] = value
    },
  })
  type Entries = Array<{ isIntersecting: boolean }>
  const observers: Array<(entries: Entries) => void> = []
  class Observer {
    constructor(callback: (entries: Entries) => void) {
      observers.push(callback)
    }
    observe() {}
    disconnect() {
      disconnects++
    }
  }
  const win = Object.assign(new EventTarget(), {
    devicePixelRatio: 3,
    innerHeight: 800,
    matchMedia: () => media,
    getComputedStyle: () => ({ color: 'rgb(255, 255, 255)' }),
    requestAnimationFrame: (fn: FrameRequestCallback) => {
      frames.set(++nextFrame, fn)
      return nextFrame
    },
    cancelAnimationFrame: (id: number) => {
      frames.delete(id)
    },
    ResizeObserver: Observer,
    IntersectionObserver: Observer,
    MutationObserver: Observer,
  })
  const root = {
    ownerDocument: Object.assign(doc, {
      defaultView: win,
      documentElement: {},
    }),
    dataset: {} as Record<string, string>,
    querySelector: (selector: string) =>
      selector === 'canvas' ? canvas : toggle,
  }
  return {
    root,
    canvas,
    toggle,
    media,
    doc,
    win,
    frames,
    observers,
    positions,
    mount: () => mountL0TokenCloud(root as unknown as HTMLElement),
    step(time: number) {
      const callbacks = [...frames.values()]
      frames.clear()
      callbacks.forEach((callback) => callback(time))
    },
    get paints() {
      return paints
    },
    get reads() {
      return reads
    },
    get disconnects() {
      return disconnects
    },
  }
}

test('deterministic bounded geometry with a smaller mobile budget', () => {
  assert.deepEqual(createL0Tokens(760), createL0Tokens(760))
  assert.equal(createL0Tokens(390).length, 320)
  assert.equal(createL0Tokens(760).length, 780)
  for (const token of createL0Tokens(760)) {
    assert.match(token.glyph, /^[\x20-\x7e]*$/)
    for (const time of [0, 120_000, 900_000]) {
      const point = projectL0Token(token, time)
      assert.ok(Number.isFinite(point.x) && Math.abs(point.x) < 2)
      assert.ok(Number.isFinite(point.y) && Math.abs(point.y) < 2)
      assert.ok(point.depth >= 0 && point.depth <= 1)
    }
  }
})

test('DPR and draw rate are bounded with no per-frame layout reads', () => {
  const f = fixture()
  const dispose = f.mount()
  assert.equal(f.canvas.width, 1520)
  assert.equal(f.canvas.height, 600)
  const reads = f.reads
  f.step(100)
  const paints = f.paints
  f.step(116)
  assert.equal(f.paints, paints)
  f.step(134)
  assert.equal(f.paints, paints + 1)
  assert.equal(f.reads, reads)
  assert.equal(f.frames.size, 1)
  dispose()
})

test('pause, hidden and offscreen states stop then resume one loop', () => {
  const f = fixture()
  const dispose = f.mount()
  f.toggle.dispatchEvent(new Event('click'))
  assert.equal(f.frames.size, 0)
  assert.equal(f.toggle.attrs['aria-pressed'], 'true')
  f.doc.hidden = true
  f.toggle.dispatchEvent(new Event('click'))
  assert.equal(f.frames.size, 0)
  f.doc.hidden = false
  f.doc.dispatchEvent(new Event('visibilitychange'))
  assert.equal(f.frames.size, 1)
  f.observers[1]([{ isIntersecting: false }])
  assert.equal(f.frames.size, 0)
  f.observers[1]([{ isIntersecting: true }])
  assert.equal(f.frames.size, 1)
  dispose()
})

test('reduced motion draws statically and reacts to preference changes', () => {
  const f = fixture({ reduced: true })
  const dispose = f.mount()
  assert.equal(f.frames.size, 0)
  assert.equal(f.toggle.disabled, true)
  assert.ok(f.paints > 0)
  f.media.matches = false
  f.media.dispatchEvent(new Event('change'))
  assert.equal(f.frames.size, 1)
  f.media.matches = true
  f.media.dispatchEvent(new Event('change'))
  assert.equal(f.frames.size, 0)
  dispose()
})

test('unmount removes observers, listeners and frames; remount is clean', () => {
  const f = fixture()
  f.mount()()
  assert.equal(f.frames.size, 0)
  assert.equal(f.disconnects, 3)
  assert.equal(f.root.dataset.cloudReady, undefined)
  const paints = f.paints
  f.win.dispatchEvent(new Event('resize'))
  f.doc.dispatchEvent(new Event('visibilitychange'))
  f.toggle.dispatchEvent(new Event('click'))
  assert.equal(f.paints, paints)
  const dispose = f.mount()
  assert.equal(f.frames.size, 1)
  f.toggle.dispatchEvent(new Event('click'))
  assert.equal(f.frames.size, 0)
  dispose()
})

test('canvas failure preserves the SVG fallback without scheduling work', () => {
  const f = fixture({ contextAvailable: false })
  f.mount()()
  assert.equal(f.root.dataset.cloudReady, undefined)
  assert.equal(f.frames.size, 0)
})

test('pointer attraction works without consuming touch scrolling', () => {
  const f = fixture()
  const dispose = f.mount()
  f.step(100)
  const event = (pointerType: string) =>
    Object.assign(new Event('pointermove'), {
      pointerType,
      clientX: 470,
      clientY: 180,
    })
  const touch = event('touch')
  f.canvas.dispatchEvent(touch)
  assert.equal(touch.defaultPrevented, false)
  f.step(134)
  const withoutPointer = f.positions.map((p) => [...p])
  f.canvas.dispatchEvent(event('mouse'))
  f.step(168)
  assert.ok(
    f.positions.some(
      (p, i) =>
        Math.hypot(p[0] - withoutPointer[i][0], p[1] - withoutPointer[i][1]) > 1
    )
  )
  dispose()
})

test('scene morphs stay bounded and continuous between the three shapes', () => {
  for (const token of createL0Tokens(390)) {
    const chat = projectL0Token(token, 100)
    assert.deepEqual(chat, projectL0Token(token, 100, 0, 0))
    for (const scene of [0, 0.5, 0.9999, 1, 1.0001, 1.5, 2]) {
      const p = projectL0Token(token, 100, 0, scene)
      const next = projectL0Token(token, 100, 0, scene + 0.0001)
      assert.ok(Number.isFinite(p.x) && Math.abs(p.x) < 2)
      assert.ok(Number.isFinite(p.y) && Math.abs(p.y) < 2)
      assert.ok(Math.hypot(p.x - next.x, p.y - next.y) < 0.002)
    }
  }
})

test('scene changes under reduced motion redraw the correct static shape without a frame loop', () => {
  const f = fixture({ reduced: true })
  const dispose = f.mount()
  const chat = f.positions.map((p) => [...p])
  f.root.dataset.cloudScene = 'explore'
  f.observers[2]([])
  assert.notDeepEqual(f.positions, chat)
  assert.equal(f.frames.size, 0)
  const explore = f.positions.map((p) => [...p])
  f.root.dataset.cloudScene = 'access'
  f.observers[2]([])
  assert.notDeepEqual(f.positions, explore)
  assert.equal(f.frames.size, 0)
  dispose()
  assert.equal(f.disconnects, 3)
})

test('letters populate all three lobes and the cloud has a broad silhouette', () => {
  for (const width of [390, 760]) {
    const tokens = createL0Tokens(width)
    assert.equal(
      new Set(tokens.filter((token) => token.glyph).map((token) => token.group))
        .size,
      3
    )
    const points = tokens.map((token) => projectL0Token(token, 0))
    const span =
      Math.max(...points.map((point) => point.x)) -
      Math.min(...points.map((point) => point.x))
    assert.ok(span > 2.2)
  }
})
