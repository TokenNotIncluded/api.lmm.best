/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { posterPalette } from './home-poster'
import {
  getSculpture,
  HOME_SEQUENCES,
  HOLD_SECONDS,
  MORPH_SECONDS,
  SCENE_SECONDS,
  sequenceAt,
} from './home-sculptures'
import type { Vec3 } from './sculptures/geometry'

test('five independent sequences cover all requested objects without a future lotus', () => {
  assert.deepEqual(
    HOME_SEQUENCES.map((list) => list.length),
    [6, 3, 6, 7, 3]
  )
  assert.equal(new Set(HOME_SEQUENCES.flat()).size, 25)
  assert.deepEqual(HOME_SEQUENCES[4], [
    'pelicanBicycle',
    'emperorBear',
    'catBomb',
  ])
})

test('every object holds, disperses and loops back to its first object', () => {
  for (const [chapter, list] of HOME_SEQUENCES.entries()) {
    for (const [index, id] of list.entries()) {
      const start = index * SCENE_SECONDS
      assert.equal(sequenceAt(chapter, start).from, id)
      assert.equal(sequenceAt(chapter, start + HOLD_SECONDS / 2).mix, 0)
      const halfway = sequenceAt(
        chapter,
        start + HOLD_SECONDS + MORPH_SECONDS / 2
      )
      assert.ok(Math.abs(halfway.mix - 0.5) < 1e-12)
      assert.equal(halfway.to, list[(index + 1) % list.length])
      const last = sequenceAt(chapter, start + SCENE_SECONDS - 1e-6)
      assert.ok(last.mix > 0.999999)
    }
    const wrapped = sequenceAt(chapter, list.length * SCENE_SECONDS + 1e-6)
    assert.equal(wrapped.from, list[0])
    assert.equal(wrapped.mix, 0)
  }
})

test('invalid and negative clocks cannot produce missing geometry', () => {
  for (const chapter of [-4, Number.NaN, Infinity, 0, 4, 20]) {
    for (const time of [-1, Number.NaN, Infinity, 0, 1e10]) {
      const frame = sequenceAt(chapter, time)
      assert.ok(frame.mix >= 0 && frame.mix <= 1)
      assert.ok(getSculpture(frame.from).points.length)
      assert.ok(getSculpture(frame.to).points.length)
    }
  }
})

for (const id of HOME_SEQUENCES.flat()) {
  test(`${id}: finite geometry, actual motion and immutable shared samples`, () => {
    const model = getSculpture(id)
    assert.ok(model.points.length >= 1000)
    assert.ok(model.points.length < 100000)
    const snapshots = model.points.map((p) => [p.x, p.y, p.z])
    for (const point of model.points) {
      assert.ok(
        [point.x, point.y, point.z, ...point.color].every(Number.isFinite)
      )
    }
    const initial = model.animate(0)
    let moved = 0
    for (const time of [1.37, 4.2, 19.2]) {
      const animate = model.animate(time)
      for (let i = 0; i < model.points.length; i += 23) {
        const point = model.points[i]
        const a: Vec3 = [point.x, point.y, point.z]
        const b: Vec3 = [...a]
        initial(point, a)
        animate(point, b)
        assert.ok(
          b.every((n) => Number.isFinite(n) && Math.abs(n) < 4.5),
          `${id}: ${b}`
        )
        if (Math.hypot(...a.map((v, axis) => v - b[axis])) > 1e-4) moved++
      }
    }
    assert.ok(moved > 10, `${id} must have object motion, not only transitions`)
    assert.deepEqual(
      model.points.map((p) => [p.x, p.y, p.z]),
      snapshots
    )
  })
}

test('light and dark dot fringes and trails blend into their own ground', () => {
  for (const ground of [
    [7, 7, 7],
    [246, 244, 240],
  ] satisfies Vec3[]) {
    const theme = posterPalette(ground)
    const ink = theme.ink([233, 150, 181])
    assert.equal(theme.coverage(ink, 0), theme.background)
    assert.equal(theme.coverage(ink, 1), ink)
    const edge = theme.coverage(ink, 0.12)
    for (const shift of [0, 8, 16]) {
      const value = (edge >>> shift) & 255
      const base = (theme.background >>> shift) & 255
      const dot = (ink >>> shift) & 255
      assert.ok(value >= Math.min(base, dot) && value <= Math.max(base, dot))
    }
  }
})
