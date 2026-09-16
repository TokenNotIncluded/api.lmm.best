/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

import { pointerPosition, storyPosition, unit } from './home-motion.ts'

describe('home motion geometry', () => {
  it('clamps finite progress without propagating invalid measurements', () => {
    assert.equal(unit(-2), 0)
    assert.equal(unit(0.5), 0.5)
    assert.equal(unit(2), 1)
    assert.equal(unit(Number.NaN), 0)
    assert.equal(unit(Number.POSITIVE_INFINITY), 0)
  })
  it('centers pointer coordinates in the scene, not the viewport', () => {
    assert.deepEqual(
      pointerPosition(400, 450, {
        left: 200,
        top: 300,
        width: 400,
        height: 300,
      }),
      { x: 0, y: 0 }
    )
  })
  it('bounds pointer movement outside the scene', () => {
    assert.deepEqual(
      pointerPosition(-20, 1000, {
        left: 200,
        top: 300,
        width: 400,
        height: 300,
      }),
      { x: -0.5, y: 0.5 }
    )
  })
  it('handles zero-sized and invalid geometry', () => {
    const position = pointerPosition(0, 0, {
      left: 0,
      top: 0,
      width: 0,
      height: 0,
    })
    assert.ok(Number.isFinite(position.x) && Number.isFinite(position.y))
    assert.equal(storyPosition(0, 0, 800), 1)
    assert.equal(storyPosition(Number.NaN, 400, 800), 0)
  })
  it('tracks section progress from actual layout offsets', () => {
    assert.equal(storyPosition(800, 900, 800), 0)
    assert.equal(storyPosition(400, 900, 800), 0)
    assert.equal(storyPosition(-50, 900, 800), 0.5)
    assert.equal(storyPosition(-500, 900, 800), 1)
  })
  it('is monotonic as the page moves through the story', () => {
    const positions = [800, 400, 100, -200, -500, -1000].map((top) =>
      storyPosition(top, 900, 800)
    )
    assert.deepEqual(
      positions,
      [...positions].sort((a, b) => a - b)
    )
  })
})
