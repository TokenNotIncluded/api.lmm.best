/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { trackMobileScroll } from './mobile-scroll-controller.ts'

function tracker(max = 2000, viewport = 500) {
  let previous: Parameters<typeof trackMobileScroll>[0]
  return (top: number, nextMax = max, nextViewport = viewport) => {
    const result = trackMobileScroll(previous, {
      top,
      max: nextMax,
      viewport: nextViewport,
    })
    previous = result.track
    return result.hidden
  }
}

test('initial view and top edge show the controls', () => {
  const read = tracker()
  assert.equal(read(0), false)
  assert.equal(read(8), false)
  assert.equal(read(-20), false)
})

test('initial restored position does not count as a downward gesture', () => {
  assert.equal(tracker()(600), undefined)
})

test('small downward steps accumulate until 24 pixels', () => {
  const read = tracker()
  read(100)
  assert.equal(read(108), undefined)
  assert.equal(read(116), undefined)
  assert.equal(read(124), true)
  assert.equal(read(124), undefined)
})

test('upward steps reveal after 12 pixels without going back to the top', () => {
  const read = tracker()
  read(600)
  assert.equal(read(596), undefined)
  assert.equal(read(592), undefined)
  assert.equal(read(588), false)
})

test('direction reversal discards previous travel instead of flickering', () => {
  const read = tracker()
  read(100)
  for (const top of [108, 104, 111, 106, 113, 108]) {
    assert.equal(read(top), undefined)
  }
  assert.equal(read(132), true)
  assert.equal(read(131), undefined)
  assert.equal(read(120), false)
})

test('bottom rubber banding is clamped, not mistaken for upward scrolling', () => {
  const read = tracker(1000)
  read(970)
  assert.equal(read(1010), true)
  assert.equal(read(1040), undefined)
  assert.equal(read(1000), undefined)
  assert.equal(read(988), false)
})

test('collapsing chrome cannot turn a clamped bottom position into an up gesture', () => {
  const read = tracker(1000, 500)
  read(980)
  assert.equal(read(800, 800, 700), undefined)
  assert.equal(read(800, 800, 700), undefined)
  assert.equal(read(788, 800, 700), false)
})

test('content resizes reset the baseline without changing visibility', () => {
  const read = tracker()
  read(300)
  assert.equal(read(700, 2400), undefined)
  assert.equal(read(500, 2200), undefined)
  assert.equal(read(524, 2200), true)
})

test('short pages and negative scroll ranges stay visible', () => {
  assert.equal(tracker(0)(20), false)
  assert.equal(tracker(-200)(0), false)
})

test('two independent scrolling regions cannot share direction history', () => {
  const first = tracker()
  const second = tracker()
  first(900)
  second(0)
  assert.equal(first(924), true)
  assert.equal(second(24), true)
  assert.equal(first(912), false)
})
