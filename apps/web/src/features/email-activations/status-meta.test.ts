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
import { describe, test } from 'node:test'

import {
  canCancelHeroSmsActivation,
  canReorderHeroSmsActivation,
  getHeroSmsStatusPresentation,
  isHeroSmsActiveStatus,
} from './status-meta'

describe('HeroSMS activation status semantics', () => {
  test('polls only backend non-terminal states', () => {
    for (const status of ['pending_provider', 'active', 'reconciling', 'cancel_pending']) {
      assert.equal(isHeroSmsActiveStatus(status), true, status)
    }
    for (const status of ['completed', 'cancelled', 'refunded', 'failed']) {
      assert.equal(isHeroSmsActiveStatus(status), false, status)
    }
  })

  test('exposes cancellation and paid reorder only for valid lifecycle states', () => {
    assert.equal(canCancelHeroSmsActivation('active'), true)
    assert.equal(canCancelHeroSmsActivation('reconciling'), true)
    assert.equal(canCancelHeroSmsActivation('completed'), false)

    assert.equal(canReorderHeroSmsActivation('completed'), true)
    assert.equal(canReorderHeroSmsActivation('cancelled'), true)
    assert.equal(canReorderHeroSmsActivation('refunded'), true)
    assert.equal(canReorderHeroSmsActivation('active'), false)
  })

  test('maps exact backend states to readable labels', () => {
    const translate = ((key: string) => key) as Parameters<
      typeof getHeroSmsStatusPresentation
    >[1]
    assert.equal(
      getHeroSmsStatusPresentation('pending_provider', translate).label,
      'Pending purchase'
    )
    assert.equal(
      getHeroSmsStatusPresentation('active', translate).label,
      'Awaiting code'
    )
    assert.equal(
      getHeroSmsStatusPresentation('completed', translate).label,
      'Code received'
    )
  })
})
