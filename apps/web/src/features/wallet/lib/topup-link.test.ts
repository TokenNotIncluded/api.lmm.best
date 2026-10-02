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

import { getWalletTopupPrefill, parseWalletTopupAmount } from './topup-link'

test('wallet links only accept one bounded whole credit amount', () => {
  assert.equal(getWalletTopupPrefill('?topup_amount=25'), 25)
  assert.equal(getWalletTopupPrefill('?topup_amount=1000000'), 1_000_000)
  for (const value of ['0', '-1', '01', '1.5', '1e3', 'Infinity', 'NaN', '1000001', '', ' 25', '0x10']) {
    assert.equal(getWalletTopupPrefill(`?topup_amount=${encodeURIComponent(value)}`), null, value)
  }
  assert.equal(getWalletTopupPrefill('?topup_amount=25&topup_amount=50'), null)
  assert.equal(getWalletTopupPrefill('?topup_amount=bad', 25), null)
  assert.equal(getWalletTopupPrefill('', 25), 25)
  assert.equal(getWalletTopupPrefill(''), null)
})

test('route search ignores invalid values without making payment requests', () => {
  for (const value of [null, undefined, true, {}, [], [25], '1e3', '01', '1000001', Number.NaN, 0, -1, 1.5]) {
    assert.equal(parseWalletTopupAmount(value), undefined)
  }
  assert.equal(parseWalletTopupAmount(25), 25)
  assert.equal(parseWalletTopupAmount('25'), 25)
})
