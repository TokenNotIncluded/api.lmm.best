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
import {
  bicycle,
  CRANK,
  kneeAt,
  PEDAL_RADIUS,
  pedalAt,
} from './sculptures/bicycle'
import { palette, Shape, type Vec3 } from './sculptures/geometry'

test('five sequences include the rose and two bicycle riders', () => {
  assert.deepEqual(
    HOME_SEQUENCES.map((list) => list.length),
    [7, 3, 6, 7, 3]
  )
  assert.equal(new Set(HOME_SEQUENCES.flat()).size, 26)
  assert.equal(HOME_SEQUENCES[0].at(-1), 'rose')
  assert.deepEqual(HOME_SEQUENCES[4], [
    'pelicanBicycle',
    'emperorBicycle',
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
      assert.ok(
        sequenceAt(chapter, start + SCENE_SECONDS - 1e-6).mix > 0.999999
      )
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
const luminance = (pixel: number) =>
  [0, 8, 16].reduce((sum, shift, i) => {
    const channel = ((pixel >>> shift) & 255) / 255
    const linear =
      channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4
    return sum + linear * [0.2126, 0.7152, 0.0722][i]
  }, 0)

test('light-theme solid dots have at least 3:1 contrast for every base material', () => {
  const theme = posterPalette([246, 244, 240])
  for (const color of Object.values(palette)) {
    const contrast =
      (luminance(theme.background) + 0.05) /
      (luminance(theme.ink(color)) + 0.05)
    assert.ok(contrast >= 3, `${color}: ${contrast}`)
  }
  const bright = theme.ink([230, 130, 160])
  const shaded = theme.ink([161, 91, 112])
  assert.ok(
    luminance(bright) > luminance(shaded),
    'theme must retain surface shading'
  )
})

test('both feet stay opposite on the crank and leg lengths stay fixed for a full turn', () => {
  for (let frame = 0; frame <= 120; frame++) {
    const t = ((frame / 120) * Math.PI * 2) / 3
    const a = pedalAt(t, -1),
      b = pedalAt(t, 1)
    for (let axis = 0; axis < 3; axis++)
      assert.ok(Math.abs((a[axis] + b[axis]) / 2 - CRANK[axis]) < 1e-12)
    for (const foot of [a, b]) {
      assert.ok(
        Math.abs(
          Math.hypot(foot[0] - CRANK[0], foot[1] - CRANK[1]) - PEDAL_RADIUS
        ) < 1e-12
      )
      const hip: Vec3 = [-0.3, -0.1 + Math.sin(t * 6) * 0.012, foot[2]]
      const knee = kneeAt(hip, foot)
      const length = (p: Vec3, q: Vec3) =>
        Math.hypot(...p.map((v, i) => v - q[i]))
      assert.ok(Math.abs(length(hip, knee) - 0.39) < 1e-12)
      assert.ok(Math.abs(length(knee, foot) - 0.4) < 1e-12)
    }
  }
})

test('shared bicycle keeps pedal and shoe offsets unchanged through the full cycle', () => {
  const shape = new Shape()
  const ride = bicycle(shape, palette.gold, palette.ink, 0.05)
  for (const side of [-1, 1]) {
    const index = side < 0 ? 0 : 1
    const parts = shape.points.filter(
      (p) => p.part === 4 + index || p.part === 14 + index
    )
    const rest = pedalAt(0, side)
    for (let frame = 0; frame <= 60; frame++) {
      const t = ((frame / 60) * Math.PI * 2) / 3,
        pose = ride(t),
        foot = pedalAt(t, side)
      for (let i = 0; i < parts.length; i += 17) {
        const p = parts[i],
          v: Vec3 = [p.x, p.y, p.z]
        pose(p, v)
        assert.ok(Math.abs(v[0] - foot[0] - (p.x - rest[0])) < 1e-12)
        assert.ok(Math.abs(v[1] - foot[1] - (p.y - rest[1])) < 1e-12)
      }
    }
  }
})

test('gyroscope rings tilt out of their original planes, not just spin in place', () => {
  const model = getSculpture('gyroscope'),
    pose = model.animate(2)
  let min = Infinity,
    max = -Infinity
  for (const point of model.points) {
    if (point.part !== 1) continue
    const v: Vec3 = [point.x, point.y, point.z]
    pose(point, v)
    min = Math.min(min, v[0])
    max = Math.max(max, v[0])
  }
  assert.ok(max - min > 0.5)
})

test('fish and helix stay inside phone and desktop frames through their motion', () => {
  for (const id of ['fish', 'helix'] as const) {
    const model = getSculpture(id)
    const [yaw, pitch] = model.view ?? [0, 0]
    for (const [width, height] of [
      [390, 490],
      [900, 720],
    ]) {
      const scale = Math.min(width * 0.34, height * 0.35)
      for (let frame = 0; frame <= 24; frame++) {
        const pose = model.animate(frame * 0.3)
        for (let i = 0; i < model.points.length; i += 17) {
          const p = model.points[i],
            v: Vec3 = [p.x, p.y, p.z]
          pose(p, v)
          const x = v[0] * Math.cos(yaw) + v[2] * Math.sin(yaw)
          const rz = v[2] * Math.cos(yaw) - v[0] * Math.sin(yaw)
          const y = v[1] * Math.cos(pitch) + rz * Math.sin(pitch)
          const z = rz * Math.cos(pitch) - v[1] * Math.sin(pitch)
          const perspective = 3.9 / (3.9 - z * 0.42)
          const px = width / 2 + x * scale * perspective
          const py = height * 0.43 - y * scale * perspective
          assert.ok(
            px > 3 && px < width - 3 && py > 3 && py < height - 3,
            `${id}: ${px}, ${py}`
          )
        }
      }
    }
  }
})
