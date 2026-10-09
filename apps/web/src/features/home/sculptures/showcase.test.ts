/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { Vec3 } from './geometry'
import { claudeMark, openaiMark } from './marks'
import { moonFarSide } from './moon-far-side'
import { atomicExplosion, rotatingChair, spacexRocket } from './spectacle'
import { blueWhale } from './whale'

const factories = {
  blueWhale,
  claudeMark,
  openaiMark,
  moonFarSide,
  spacexRocket,
  atomicExplosion,
  rotatingChair,
}

for (const [id, factory] of Object.entries(factories)) {
  test(`${id}: bounded motion and deterministic, immutable samples`, () => {
    const model = factory()
    const snapshot = JSON.stringify(model.points)
    assert.ok(model.points.length >= 1000 && model.points.length < 40000)
    assert.equal(JSON.stringify(factory().points), snapshot)
    for (const p of model.points) {
      assert.ok([p.x, p.y, p.z, ...p.color].every(Number.isFinite))
    }
    const initial = model.animate(0)
    const [yaw, pitch] = model.view ?? [0, 0]
    let moved = 0
    for (let frame = 0; frame <= 80; frame++) {
      const time = frame * 0.3
      const pose = model.animate(time)
      for (let i = 0; i < model.points.length; i += 19) {
        const p = model.points[i]
        const v: Vec3 = [p.x, p.y, p.z]
        const rest: Vec3 = [...v]
        pose(p, v)
        initial(p, rest)
        assert.ok(v.every((n) => Number.isFinite(n) && Math.abs(n) < 1.65))
        if (Math.hypot(...v.map((n, axis) => n - rest[axis])) > 1e-4) moved++
        for (const [width, height] of [
          [360, 420],
          [390, 490],
          [900, 720],
        ]) {
          const x = v[0] * Math.cos(yaw) + v[2] * Math.sin(yaw)
          const rz = v[2] * Math.cos(yaw) - v[0] * Math.sin(yaw)
          const y = v[1] * Math.cos(pitch) + rz * Math.sin(pitch)
          const z = rz * Math.cos(pitch) - v[1] * Math.sin(pitch)
          const scale =
            (Math.min(width * 0.34, height * 0.35) * 3.9) / (3.9 - z * 0.42)
          const px = width / 2 + x * scale
          const py = height * 0.43 - y * scale
          assert.ok(
            px > 3 && px < width - 3 && py > 3 && py < height - 3,
            `${id} clips ${width}x${height} at ${time}: ${px}, ${py}`
          )
        }
      }
    }
    assert.ok(moved > 10)
    assert.equal(JSON.stringify(model.points), snapshot)
  })
}

test('OpenAI keeps its central opening and six surrounding lobes', () => {
  const model = openaiMark()
  assert.ok(!model.points.some((p) => Math.hypot(p.x, p.y) < 0.13))
  for (let i = 0; i < 6; i++) {
    const angle = i * Math.PI / 3
    assert.ok(
      model.points.some(
        (p) =>
          Math.hypot(
            p.x - Math.cos(angle) * 0.68,
            p.y - Math.sin(angle) * 0.68
          ) < 0.23
      )
    )
  }
})

test('far-side craters change the surface, not only its color', () => {
  const radii = moonFarSide().points.map((p) => Math.hypot(p.x, p.y, p.z))
  assert.ok(Math.max(...radii) - Math.min(...radii) > 0.04)
})
