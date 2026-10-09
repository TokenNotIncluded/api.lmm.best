/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { observeL0Transcript } from './l0-transcript-scroll'

class Pane extends EventTarget {
  clientHeight = 400
  scrollHeight = 1800
  private position = 0
  get scrollTop() {
    return Math.min(
      this.position,
      Math.max(0, this.scrollHeight - this.clientHeight)
    )
  }
  set scrollTop(value: number) {
    this.position = Math.max(
      0,
      Math.min(value, this.scrollHeight - this.clientHeight)
    )
  }
  scroll(value: number) {
    this.scrollTop = value
    this.dispatchEvent(new Event('scroll'))
  }
}

function setup() {
  const pane = new Pane()
  const changes: boolean[] = []
  const control = observeL0Transcript(pane as unknown as HTMLElement, (away) =>
    changes.push(away)
  )
  return { pane, changes, control }
}

test('keyboard open and close keep following the last reply', () => {
  const { pane, changes, control } = setup()
  assert.equal(pane.scrollTop, 1400)
  for (const height of [180, 140, 400, 230, 400]) {
    pane.clientHeight = height
    pane.dispatchEvent(new Event('scroll'))
    control.sync()
    assert.equal(pane.scrollTop, pane.scrollHeight - height)
  }
  assert.deepEqual(changes, [])
  control.dispose()
})

test('reading old text survives keyboard resize and streamed content', () => {
  const { pane, changes, control } = setup()
  pane.scroll(320)
  for (const height of [150, 400, 200]) {
    pane.clientHeight = height
    pane.scrollHeight += 200
    control.sync()
    pane.dispatchEvent(new Event('scroll'))
    assert.equal(pane.scrollTop, 320)
  }
  assert.deepEqual(changes, [true])
  control.latest()
  assert.equal(pane.scrollTop, pane.scrollHeight - pane.clientHeight)
  assert.deepEqual(changes, [true, false])
  control.dispose()
})

test('temporary clamping does not turn a reading anchor into follow mode', () => {
  const { pane, changes, control } = setup()
  pane.scroll(1200)
  pane.clientHeight = 900
  control.sync()
  assert.equal(pane.scrollTop, 900)
  pane.dispatchEvent(new Event('scroll'))
  pane.clientHeight = 400
  control.sync()
  assert.equal(pane.scrollTop, 1200)
  assert.deepEqual(changes, [true])
  control.dispose()
})

test('hidden tabs retain reading position and the jump action restores following', () => {
  const { pane, changes, control } = setup()
  pane.scroll(200)
  pane.clientHeight = 0
  control.sync()
  pane.dispatchEvent(new Event('scroll'))
  pane.clientHeight = 400
  control.sync()
  assert.equal(pane.scrollTop, 200)
  pane.scroll(1400)
  pane.scrollHeight += 500
  control.sync()
  assert.equal(pane.scrollTop, 1900)
  assert.deepEqual(changes, [true, false])
  control.dispose()
})

test('disposing stops scroll notifications', () => {
  const { pane, changes, control } = setup()
  control.dispose()
  pane.scroll(0)
  assert.deepEqual(changes, [])
})

test('resize observation covers the viewport and markdown content, then disconnects', () => {
  const original = globalThis.ResizeObserver
  let callback = () => {}
  const observed: unknown[] = []
  let disconnected = false
  globalThis.ResizeObserver = class {
    constructor(onResize: () => void) {
      callback = onResize
    }
    observe(element: unknown) {
      observed.push(element)
    }
    disconnect() {
      disconnected = true
    }
  } as unknown as typeof ResizeObserver
  try {
    const pane = new Pane()
    const content = {} as HTMLElement
    const control = observeL0Transcript(
      pane as unknown as HTMLElement,
      () => {},
      content
    )
    assert.deepEqual(observed, [pane, content])
    pane.clientHeight = 200
    callback()
    assert.equal(pane.scrollTop, 1600)
    pane.scrollHeight += 100
    callback()
    assert.equal(pane.scrollTop, 1700)
    control.dispose()
    assert.equal(disconnected, true)
  } finally {
    globalThis.ResizeObserver = original
  }
})
