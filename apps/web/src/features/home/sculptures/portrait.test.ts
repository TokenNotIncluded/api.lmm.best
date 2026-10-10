/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { Point, Sculpture, Vec3 } from './geometry'
import { PORTRAIT_PERIOD, portraitExpressionAt, smilingPortrait } from './portrait'
import { blueWhale } from './whale'

function posed(model: Sculpture, point: Point, seconds: number): Vec3 {
  const v: Vec3 = [point.x, point.y, point.z]
  model.animate(seconds)(point, v)
  return v
}

for (const [name, factory] of Object.entries({ smilingPortrait, blueWhale })) {
  test(`${name}: bounded, repeatable geometry fits the phone particle budget`, () => {
    const a = factory(), b = factory()
    assert.ok(a.points.length > 1000 && a.points.length <= 22000)
    assert.deepEqual(a.points, b.points)
    for (const point of a.points) {
      assert.ok([point.x, point.y, point.z, ...point.color].every(Number.isFinite))
      assert.ok(point.color.every((channel) => channel >= 0 && channel <= 255))
    }
  })

  test(`${name}: all animated samples stay immutable through a full display slot`, () => {
    const model = factory()
    const original = structuredClone(model.points)
    for (const time of [0, 1.37, 2.4, 2.8, 4.2, 6.55, 9.6, 12]) {
      const animate = model.animate(time)
      for (const point of model.points) {
        const v: Vec3 = [point.x, point.y, point.z]
        animate(point, v)
        assert.ok(v.every((n) => Number.isFinite(n) && Math.abs(n) < 1.6))
      }
    }
    assert.deepEqual(model.points, original)
  })

  test(`${name}: projected bounds include every point at phone and desktop sizes`, () => {
    const model = factory()
    const [yaw, pitch] = model.view ?? [0, 0]
    for (const [width, height] of [[320, 440], [390, 490], [900, 720]]) {
      const scale = Math.min(width * 0.34, height * 0.35)
      for (let frame = 0; frame <= 32; frame++) {
        const animate = model.animate(frame * 0.375)
        for (const point of model.points) {
          const v: Vec3 = [point.x, point.y, point.z]
          animate(point, v)
          const x = v[0] * Math.cos(yaw) + v[2] * Math.sin(yaw)
          const rz = v[2] * Math.cos(yaw) - v[0] * Math.sin(yaw)
          const y = v[1] * Math.cos(pitch) + rz * Math.sin(pitch)
          const z = rz * Math.cos(pitch) - v[1] * Math.sin(pitch)
          const perspective = 3.9 / (3.9 - z * 0.42)
          const px = width / 2 + x * scale * perspective
          const py = height * 0.43 - y * scale * perspective
          assert.ok(px > 3 && px < width - 3 && py > 3 && py < height - 3, `${name}: ${px}, ${py}`)
        }
      }
    }
  })
}

test('portrait expression is finite, periodic and starts visibly smiling', () => {
  for (const seconds of [0, 0.9, 2.4, 2.8, 4.2, 6.55]) {
    const a = portraitExpressionAt(seconds)
    const b = portraitExpressionAt(seconds + PORTRAIT_PERIOD)
    assert.ok(a.smile >= 0.3 && a.smile <= 1)
    assert.ok(a.blink >= 0 && a.blink <= 1)
    assert.ok(Math.abs(a.smile - b.smile) < 1e-12)
    assert.ok(Math.abs(a.blink - b.blink) < 1e-12)
  }
  assert.ok(portraitExpressionAt(2.4).smile > 0.7)
  assert.ok(portraitExpressionAt(2.8).blink > 0.99)
  assert.equal(portraitExpressionAt(3.2).blink, 0)
  for (const time of [-3, NaN, Infinity, -Infinity]) {
    assert.deepEqual(portraitExpressionAt(time), portraitExpressionAt(0))
  }
})

test('portrait has real front-to-back depth and no rectangular wall of particles', () => {
  const model = smilingPortrait()
  const depths = model.points.map((p) => p.z)
  assert.ok(Math.max(...depths) - Math.min(...depths) > 0.5)
  assert.ok(model.points.some((p) => p.part === 2 && p.z < 0))
  const backgroundCorners = model.points.filter((p) => p.y > 0.85 && Math.abs(p.x) > 0.65)
  assert.equal(backgroundCorners.length, 0)
})

test('portrait smile changes the mouth relative to the face, not just the whole bust', () => {
  const model = smilingPortrait()
  const local = (x: number, y: number) => model.points.reduce((a, b) =>
    Math.hypot(b.x - x, b.y - y) < Math.hypot(a.x - x, a.y - y) ? b : a
  )
  const left = local(-0.21, 0.04), right = local(0.17, -0.01)
  const distance = (a: Vec3, b: Vec3) => Math.hypot(...a.map((n, i) => n - b[i]))
  const relaxed = distance(posed(model, left, 0), posed(model, right, 0))
  const smiling = distance(posed(model, left, 4.2), posed(model, right, 4.2))
  assert.ok(smiling > relaxed + 0.01)
})

test('portrait full pose has no visible jump at the expression loop boundary', () => {
  const model = smilingPortrait()
  for (let i = 0; i < model.points.length; i += 13) {
    const a = posed(model, model.points[i], 1.37)
    const b = posed(model, model.points[i], 1.37 + PORTRAIT_PERIOD)
    assert.ok(Math.hypot(...a.map((n, j) => n - b[j])) < 1e-12)
  }
})

test('whale flukes are horizontal and the dorsal fin stays close to the back', () => {
  const model = blueWhale()
  const flukes = model.points.filter((p) => p.part === 1)
  const span = (axis: 'x' | 'y' | 'z') => Math.max(...flukes.map((p) => p[axis])) - Math.min(...flukes.map((p) => p[axis]))
  assert.ok(span('z') > 1)
  assert.ok(span('y') < 0.04)
  // The swept front edge may overlap the tail stock, but stays in the rear quarter.
  assert.ok(flukes.every((p) => p.x < -0.9))
  const fin = model.points.filter((p) => p.part === 0 && p.x < -0.34 && p.x > -0.68 && Math.abs(p.z) < 0.015)
  assert.ok(fin.length > 100)
  assert.ok(Math.max(...fin.map((p) => p.y)) < 0.4)
})
