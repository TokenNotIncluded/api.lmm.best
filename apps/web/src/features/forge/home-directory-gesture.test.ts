/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

import { Window } from 'happy-dom'

import {
  attachDirectoryGesture,
  directoryProgress,
  directoryWheelDistance,
} from './home-directory-gesture'

function fixture() {
  const view = new Window({ url: 'https://example.test/' })
  const doc = view.document
  doc.body.innerHTML =
    '<aside><a href="/ai-directory">Directory</a></aside><main></main>'
  Object.defineProperty(doc, 'hidden', { value: false })
  const entry = doc.querySelector('aside')!
  entry.getBoundingClientRect = () => new view.DOMRect(0, 64, 800, 64)
  const frames = new Map<ReturnType<typeof view.requestAnimationFrame>, FrameRequestCallback>()
  let nextFrame = 0
  view.requestAnimationFrame = (callback) => {
    const frameID = (++nextFrame) as unknown as ReturnType<typeof view.requestAnimationFrame>
    frames.set(frameID, callback)
    return frameID
  }
  view.cancelAnimationFrame = (id) => {
    frames.delete(id)
  }
  let progress = 0
  let navigations = 0
  const dispose = attachDirectoryGesture(entry as unknown as HTMLElement, {
    onProgress: (value) => {
      progress = value
    },
    onNavigate: () => {
      navigations++
    },
  })
  const wheel = (
    deltaY: number,
    options: WheelEventInit = {},
    target = doc.body
  ) => {
    const event = new view.WheelEvent('wheel', {
      bubbles: true,
      cancelable: true,
      deltaY,
      ...options,
    } as ConstructorParameters<typeof view.WheelEvent>[1])
    target.dispatchEvent(event)
    return event
  }
  const touch = (type: string, y: number, x = 40, count = 1) => {
    const points = Array.from({ length: count }, (_, id) => ({
      identifier: id,
      clientX: x + id,
      clientY: y,
    }))
    const event = Object.assign(
      new view.Event(type, { bubbles: true, cancelable: true }),
      {
        touches: type === 'touchend' ? [] : points,
        changedTouches: points,
      }
    )
    entry.dispatchEvent(event)
    return event
  }
  const paint = async () => {
    for (let i = 0; i < 2; i++) {
      const callbacks = [...frames.values()]
      frames.clear()
      for (const callback of callbacks) callback(0)
    }
    await Promise.resolve()
    await Promise.resolve()
  }
  return {
    view,
    doc,
    entry,
    wheel,
    touch,
    paint,
    dispose,
    progress: () => progress,
    navigations: () => navigations,
  }
}

test('wheel units, large ticks and invalid input stay bounded', () => {
  assert.equal(directoryWheelDistance(-20, 0, 800), 20)
  assert.equal(directoryWheelDistance(-2, 1, 800), 32)
  assert.equal(directoryWheelDistance(-1, 2, 800), 80)
  assert.equal(directoryWheelDistance(-10000, 0, 800), 80)
  assert.equal(directoryWheelDistance(20, 0, 800), -20)
  assert.equal(directoryWheelDistance(Number.NaN, 0, 800), 0)
  assert.equal(directoryProgress(-2, 100), 0)
  assert.equal(directoryProgress(50, 100), 0.5)
  assert.equal(directoryProgress(500, 100), 1)
  assert.equal(directoryProgress(Infinity, 100), 0)
  assert.equal(directoryProgress(50, 0), 0)
})

test('the top-boundary wheel gesture completes once despite trailing wheel events', async () => {
  const f = fixture()
  try {
    assert.equal(f.wheel(-120).defaultPrevented, true)
    assert.equal(f.progress(), 1 / 3)
    f.wheel(-120)
    assert.equal(f.navigations(), 0)
    f.wheel(-120)
    f.wheel(-120)
    await f.paint()
    assert.equal(f.progress(), 1)
    assert.equal(f.navigations(), 1)
  } finally {
    f.dispose()
  }
})

test('reversing scroll cancels progress without blocking ordinary scrolling', () => {
  const f = fixture()
  try {
    f.wheel(-80)
    assert.equal(f.wheel(50).defaultPrevented, false)
    assert.equal(f.progress(), 0)
    assert.equal(f.wheel(-80, { ctrlKey: true }).defaultPrevented, false)
    assert.equal(f.wheel(-20, { deltaX: 100 }).defaultPrevented, false)
    assert.equal(f.progress(), 0)
  } finally {
    f.dispose()
  }
})

test('modals and scrolled pages retain their own scrolling', () => {
  const f = fixture()
  try {
    const dialog = f.doc.createElement('div')
    dialog.setAttribute('role', 'dialog')
    dialog.setAttribute('aria-modal', 'true')
    f.doc.body.append(dialog)
    assert.equal(f.wheel(-80).defaultPrevented, false)
    dialog.remove()
    Object.defineProperty(f.view, 'scrollY', { value: 100 })
    assert.equal(f.wheel(-80).defaultPrevented, false)
    assert.equal(f.progress(), 0)
  } finally {
    f.dispose()
  }
})

test('touch progress follows the swipe and opens only after a completed release', async () => {
  const f = fixture()
  try {
    f.touch('touchstart', 120)
    assert.equal(f.touch('touchmove', 84).defaultPrevented, true)
    assert.equal(f.progress(), 0.5)
    f.touch('touchmove', 40)
    await f.paint()
    assert.equal(f.navigations(), 0)
    f.touch('touchend', 40)
    await f.paint()
    assert.equal(f.navigations(), 1)
  } finally {
    f.dispose()
  }
})

test('short, horizontal, cancelled and multi-touch gestures do not navigate', async () => {
  const f = fixture()
  try {
    f.touch('touchstart', 120)
    f.touch('touchmove', 100)
    f.touch('touchend', 100)
    assert.equal(f.progress(), 0)
    f.touch('touchstart', 120)
    assert.equal(f.touch('touchmove', 110, 200).defaultPrevented, false)
    f.touch('touchstart', 120)
    f.touch('touchmove', 40)
    f.touch('touchcancel', 40)
    f.touch('touchstart', 120, 40, 2)
    f.touch('touchmove', 40, 40, 2)
    await f.paint()
    assert.equal(f.navigations(), 0)
    assert.equal(f.progress(), 0)
  } finally {
    f.dispose()
  }
})

test('unmount cancels pending route changes and removes input listeners', async () => {
  const f = fixture()
  f.wheel(-80)
  f.wheel(-80)
  f.wheel(-80)
  f.dispose()
  await f.paint()
  assert.equal(f.navigations(), 0)
  assert.equal(f.wheel(-80).defaultPrevented, false)
})

test('the entry exposes real progress and the hero no longer renders its caption', () => {
  const entry = readFileSync(
    new URL('./home-directory-link.tsx', import.meta.url),
    'utf8'
  )
  const hero = readFileSync(
    new URL('../home/home-landing.tsx', import.meta.url),
    'utf8'
  )
  assert.match(entry, /role='progressbar'/)
  assert.match(entry, /aria-valuenow=\{percent\}/)
  assert.match(entry, /to='\/ai-directory'/)
  assert.doesNotMatch(hero, /lmm-poster-caption/)
})
