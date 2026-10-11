/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { Vec3 } from './geometry'
import * as marks from './marks'

const expected = [
  'claudeMark',
  'openaiMark',
  'grokMark',
  'glmMark',
  'kimiMark',
  'deepseekMark',
  'qwenMark',
  'geminiMark',
  'mistralMark',
  'ollamaMark',
] as const

test('the brand registry contains only the ten requested AI mark factories', () => {
  assert.deepEqual(Object.keys(marks).sort(), [...expected].sort())
})

for (const id of expected) {
  test(`${id}: bounded 3D samples are repeatable and need no browser or network`, () => {
    const model = marks[id]()
    assert.ok(model.points.length >= 1000)
    assert.ok(model.points.length <= 20000)
    assert.deepEqual(model.points, marks[id]().points)
    let front = 0
    let back = 0
    for (const p of model.points) {
      assert.ok([p.x, p.y, p.z, ...p.color].every(Number.isFinite))
      assert.ok(Math.abs(p.x) <= 1.11 && Math.abs(p.y) <= 1.11)
      assert.ok(Math.abs(p.z) >= 0.025 && Math.abs(p.z) <= 0.091)
      assert.ok(p.color.every((v) => v >= 0 && v <= 255))
      if (p.z > 0) front++
      else back++
    }
    assert.equal(front, back)
  })

  test(`${id}: object motion preserves the cached geometry`, () => {
    const model = marks[id]()
    const before = model.points.map((p) => [p.x, p.y, p.z, ...p.color])
    const initial = model.animate(0)
    let moved = 0
    for (const time of [1.37, 4.2, 19.2, 97]) {
      const pose = model.animate(time)
      for (let i = 0; i < model.points.length; i += 23) {
        const p = model.points[i]
        const a: Vec3 = [p.x, p.y, p.z]
        const b: Vec3 = [...a]
        initial(p, a)
        pose(p, b)
        assert.ok(b.every(Number.isFinite))
        if (Math.hypot(...a.map((v, axis) => v - b[axis])) > 1e-4) moved++
      }
    }
    assert.ok(moved > 100)
    assert.deepEqual(
      model.points.map((p) => [p.x, p.y, p.z, ...p.color]),
      before
    )
  })

  test(`${id}: all sampled animation frames fit phone and desktop viewports`, () => {
    const model = marks[id]()
    const [yaw, pitch] = model.view ?? [0, 0]
    for (const [width, height] of [
      [320, 400],
      [390, 490],
      [900, 720],
    ]) {
      const scale = Math.min(width * 0.34, height * 0.35)
      for (let frame = 0; frame <= 120; frame++) {
        const pose = model.animate(frame * 0.8)
        for (let i = 0; i < model.points.length; i += 17) {
          const p = model.points[i]
          const v: Vec3 = [p.x, p.y, p.z]
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
            `${id}: ${width}x${height}: ${px}, ${py}`
          )
        }
      }
    }
  })
}

test('the OpenAI knot keeps its central hole while Claude keeps its solid center', () => {
  const center = (factory: typeof marks.claudeMark) =>
    factory().points.filter((p) => Math.hypot(p.x, p.y) < 0.2).length
  assert.equal(center(marks.openaiMark), 0)
  assert.ok(center(marks.claudeMark) > 100)
})
