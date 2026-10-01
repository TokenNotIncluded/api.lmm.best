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
import { describe, test } from 'node:test'

import { transferQuota, formatTransferQuota } from './api'

describe('wallet transfer amount conversion', () => {
  test('shows exact amounts without compact rounding', () => {
    assert.match(formatTransferQuota(625000), /1[.,]25/)
    assert.match(formatTransferQuota(1), /0[.,]000002/)
  })
  test('keeps exact platform credit units', () => {
    assert.equal(transferQuota('1.25', 500000), 625000)
    assert.equal(transferQuota('0.000002', 500000), 1)
    assert.equal(transferQuota('0.1', 500000), 50000)
  })
  test('rejects unrepresentable, unsafe and nonpositive input', () => {
    for (const amount of [
      '0',
      '-1',
      'NaN',
      'Infinity',
      '1e4',
      '0.000001',
      '1.0000001',
      '100000000000000000',
      '',
      '1,000',
    ]) {
      assert.equal(transferQuota(amount, 500000), null, amount)
    }
  })
})
