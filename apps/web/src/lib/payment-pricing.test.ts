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

import {
  usesDedicatedPaymentPricing,
  platformUnitsToUsd,
} from './payment-pricing'

test('wallet fallback uses the same USD bridge as model estimates', () => {
  assert.equal(platformUnitsToUsd(14, 14), 1)
  assert.equal(platformUnitsToUsd(1, 14), 1 / 14)
  for (const rate of [0, -1, Number.NaN, Number.POSITIVE_INFINITY]) {
    assert.ok(Number.isNaN(platformUnitsToUsd(10, rate)))
  }
})

test('built-in gateways cannot expose custom settlement pricing', () => {
  for (const type of ['stripe', 'waffo', 'waffo_pancake', 'alipay', 'wxpay']) {
    assert.equal(usesDedicatedPaymentPricing(type), true, type)
  }
  assert.equal(usesDedicatedPaymentPricing('custom_gateway'), false)
})
