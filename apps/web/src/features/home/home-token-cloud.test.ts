/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { tokenRepulsion } from './home-token-cloud'

test('token interaction is local, bounded and points away from the cursor', () => {
  const near = tokenRepulsion(110, 100, { x: 100, y: 100 })
  assert.ok(near.x > 0)
  assert.ok(near.strength > 0 && near.strength <= 1)
  assert.ok(Math.hypot(near.x, near.y) <= 24)
  const far = tokenRepulsion(600, 500, { x: 100, y: 100 })
  assert.equal(far.strength, 0)
  const centered = tokenRepulsion(100, 100, { x: 100, y: 100 }, Math.PI / 2)
  assert.ok(Number.isFinite(centered.x) && Number.isFinite(centered.y))
  assert.ok(centered.y > 0)
})

test('leaving or invalid pointer coordinates produce no force', () => {
  assert.deepEqual(tokenRepulsion(10, 20, null), { x: 0, y: 0, strength: 0 })
  assert.deepEqual(tokenRepulsion(10, 20, { x: Number.NaN, y: 0 }), {
    x: 0,
    y: 0,
    strength: 0,
  })
})
