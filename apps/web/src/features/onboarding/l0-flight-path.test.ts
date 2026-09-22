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
  l0FlightFrames,
  L0_ARRIVAL_DURATION,
  L0_MAX_FLIGHTS,
} from './l0-flight-path'

const from = { x: 120, y: 500 }
const to = { x: 240, y: 120 }

test('sampled curves have exact source and landing positions with no hard-corner waypoint', () => {
  for (const up of [true, false]) {
    const frames = l0FlightFrames(from, to, up, 0)
    assert.equal(frames.length, 13)
    assert.match(frames[0].transform, /translate3d\(120px,500px,0\)/)
    assert.match(frames.at(-1)?.transform ?? '', /translate3d\(240px,120px,0\)/)
    for (let index = 1; index < frames.length; index++) {
      assert.ok(frames[index].offset > frames[index - 1].offset)
      assert.ok(frames[index].opacity >= 0 && frames[index].opacity <= 1)
      assert.doesNotMatch(frames[index].transform, /NaN|Infinity/)
    }
    assert.equal(frames.at(-1)?.opacity, up ? 0 : 1)
  }
})

test('nearby lanes differ without changing the final text position', () => {
  const left = l0FlightFrames(from, to, true, 0)
  const right = l0FlightFrames(from, to, true, 2)
  assert.notEqual(left[3].transform, right[3].transform)
  assert.equal(left.at(-1)?.transform, right.at(-1)?.transform)
  assert.deepEqual(left, l0FlightFrames(from, to, true, 0))
})

test('motion budget stays small and response landing remains bounded', () => {
  assert.ok(L0_MAX_FLIGHTS <= 32)
  assert.ok(L0_ARRIVAL_DURATION <= 340)
  assert.match(
    l0FlightFrames(to, from, false, 1).at(-1)?.transform ?? '',
    /scale\(1\)$/
  )
})
