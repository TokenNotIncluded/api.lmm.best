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
/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { costAmountMicros } from './cost-amount'

test('distinguishes omitted spend from an explicit zero and preserves six decimals', () => {
  assert.equal(costAmountMicros(''), null)
  assert.equal(costAmountMicros('0'), 0)
  assert.equal(costAmountMicros('12.000001'), 12_000_001)
  assert.equal(costAmountMicros('0.000001'), 1)
  for (const value of [
    '-1',
    'NaN',
    '1e3',
    '0.1234567',
    '1000000000',
    '1,234',
  ]) {
    assert.equal(costAmountMicros(value), null)
  }
})
