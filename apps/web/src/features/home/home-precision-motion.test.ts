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
import { test } from 'node:test'
import { canPlayBackground, counterValue } from './home-precision-motion.ts'

const active = {
  reduced: false, saveData: false, hidden: false,
  visible: true, paused: false, dialogOpen: false,
}

test('decorative video plays only when visible and permitted', () => {
  assert.equal(canPlayBackground(active), true)
  for (const key of ['reduced', 'saveData', 'hidden', 'paused', 'dialogOpen'] as const) {
    assert.equal(canPlayBackground({ ...active, [key]: true }), false, key)
  }
  assert.equal(canPlayBackground({ ...active, visible: false }), false)
})

test('counter is finite, monotonic and bounded for every displayed inventory', () => {
  for (const target of [1, 3, 2]) {
    assert.equal(counterValue(target, -100), 0)
    let previous = 0
    for (let elapsed = 0; elapsed <= 1000; elapsed += 10) {
      const value = counterValue(target, elapsed)
      assert.ok(Number.isFinite(value))
      assert.ok(value >= previous && value <= target)
      previous = value
    }
    assert.equal(counterValue(target, 900), target)
    assert.equal(counterValue(target, 5000), target)
  }
})
