/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createL0Tokens, mountL0TokenCloud } from './l0-token-cloud'

function fixture({ reduced = false, contextAvailable = true } = {}) {
  const frames = new Map<number, FrameRequestCallback>()
  let nextFrame = 0
  let paints = 0
  let measurements = 0
  let disconnects = 0
  const positions: number[][] = []
  const context = {
    clearRect() { paints++; positions.length = 0 },
    setTransform() {},
    fillText(_text: string, x: number, y: number) { positions.push([x, y]) },
  }
  const media = Object.assign(new EventTarget(), { matches: reduced })
  const doc = Object.assign(new EventTarget(), { hidden: false })
  const canvas = Object.assign(new EventTarget(), {
    width: 0,
    height: 0,
    getContext: () => contextAvailable ? context : null,
    getBoundingClientRect: () => {
      measurements++
      return { top: 10, bottom: 310, left: 0, width: 900, height: 300 }
    },
  })
  const toggle = Object.assign(new EventTarget(), {
    disabled: false,
    attrs: {} as Record<string, string>,
    setAttribute(key: string, value: string) { this.attrs[key] = value },
  })
  const observers: Array<{ callback: (entries: Array<{ isIntersecting: boolean }>) => void }> = []
  class Observer {
    constructor(callback: (entries: Array<{ isIntersecting: boolean }>) => void) { observers.push({ callback }) }
    observe() {}
    disconnect() { disconnects++ }
  }
  const win = Object.assign(new EventTarget(), {
    devicePixelRatio: 3,
    innerHeight: 800,
    matchMedia: () => media,
    getComputedStyle: () => ({ color: 'rgb(255, 255, 255)' }),
    requestAnimationFrame: (fn: FrameRequestCallback) => { frames.set(++nextFrame, fn); return nextFrame },
    cancelAnimationFrame: (id: number) => { frames.delete(id) },
    ResizeObserver: Observer,
    IntersectionObserver: Observer,
    MutationObserver: Observer,
  })
  const root = {
    ownerDocument: Object.assign(doc, { defaultView: win, documentElement: {} }),
    dataset: {} as Record<string, string>,
    querySelector: (selector: string) => selector === 'canvas' ? canvas : toggle,
  }
  const mount = () => mountL0TokenCloud(root as unknown as HTMLElement)
  const step = (time: number) => {
    const pending = [...frames.values()]
    frames.clear()
    pending.forEach((callback) => callback(time))
  }
  return {
    root, canvas, toggle, frames, doc, media, win, observers, positions, mount, step,
    get paints() { return paints },
    get measurements() { return measurements },
    get disconnects() { return disconnects },
  }
}

test('tokens are stable, ASCII-only, bounded and reduced on mobile', () => {
  assert.deepEqual(createL0Tokens(900), createL0Tokens(900))
  assert.equal(createL0Tokens(390).length, 92)
  assert.equal(createL0Tokens(900).length, 174)
  for (const token of createL0Tokens(900)) {
    assert.match(token.glyph, /^[\x20-\x7e]+$/)
    assert.ok(token.x > 0 && token.x < 1 && token.y > 0 && token.y < 1)
    assert.ok(token.depth >= 0 && token.depth <= 1)
  }
})

test('mount caps resolution and paints at most 30fps without layout reads per frame', () => {
  const f = fixture()
  const dispose = f.mount()
  assert.equal(f.canvas.width, 1800)
  assert.equal(f.canvas.height, 600)
  const reads = f.measurements
  f.step(100)
  const count = f.paints
  f.step(116)
  assert.equal(f.paints, count)
  f.step(134)
  assert.equal(f.paints, count + 1)
  assert.equal(f.measurements, reads)
  assert.equal(f.frames.size, 1)
  dispose()
})

test('pause, visibility and offscreen states cancel frames, then resume only one loop', () => {
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
  f.observers[1].callback([{ isIntersecting: false }])
  assert.equal(f.frames.size, 0)
  f.observers[1].callback([{ isIntersecting: true }])
  assert.equal(f.frames.size, 1)
  dispose()
})

test('reduced motion is static initially and also reacts to a preference change', () => {
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

test('cleanup is complete and remounting does not accumulate listeners or frames', () => {
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

test('canvas failure leaves fallback visible and schedules no work', () => {
  const f = fixture({ contextAvailable: false })
  f.mount()()
  assert.equal(f.root.dataset.cloudReady, undefined)
  assert.equal(f.frames.size, 0)
})

test('mouse input affects the cloud but touch leaves scrolling alone', () => {
  const f = fixture()
  const dispose = f.mount()
  f.step(100)
  const event = (pointerType: string) => Object.assign(new Event('pointermove'), {
    pointerType, clientX: 450, clientY: 150,
  })
  const touch = event('touch')
  f.canvas.dispatchEvent(touch)
  assert.equal(touch.defaultPrevented, false)
  f.step(134)
  const withoutPointer = f.positions.map((p) => [...p])
  f.canvas.dispatchEvent(event('mouse'))
  f.step(168)
  assert.ok(f.positions.some((position, i) => Math.hypot(position[0] - withoutPointer[i][0], position[1] - withoutPointer[i][1]) > 1))
  dispose()
})
