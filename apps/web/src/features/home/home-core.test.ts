/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createCoreMesh, transformCorePoint } from './home-core'
import { cinemaPosition } from './home-motion'

test('the abstract Transformer remains finite across the entire reversible scroll sequence', () => {
  const mesh = createCoreMesh()
  assert.ok(mesh.length > 5000 && mesh.length < 16000)
  for (const progress of [0, 0.2, 0.4, 0.6, 0.8, 1]) {
    for (const face of mesh) {
      for (const point of face.points) {
        const transformed = transformCorePoint(point, face, progress)
        for (const value of Object.values(transformed)) {
          assert.ok(Number.isFinite(value) && Math.abs(value) < 5)
        }
      }
    }
  }
  const tensor = mesh.find((face) => Math.abs(face.points[0].z) > 0.3)
  assert.ok(tensor)
  const assembled = transformCorePoint(tensor.points[0], tensor, 0)
  const expanded = transformCorePoint(tensor.points[0], tensor, 0.55)
  assert.ok(Math.abs(expanded.z - assembled.z) > 0.5)
  const closed = transformCorePoint(tensor.points[0], tensor, 1)
  assert.equal(closed.x, assembled.x)
  assert.equal(closed.z, assembled.z)
})

test('scroll progress spans the pinned runway, not the whole section height', () => {
  assert.equal(cinemaPosition(80, 3000, 800, 80), 0)
  assert.equal(cinemaPosition(-1020, 3000, 800, 80), 0.5)
  assert.equal(cinemaPosition(-2120, 3000, 800, 80), 1)
  assert.equal(cinemaPosition(900, 3000, 800, 80), 0)
  assert.equal(cinemaPosition(-9000, 3000, 800, 80), 1)
})
