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

import { getHeroSmsPollingInterval } from './hooks'
import {
  canCancelHeroSmsActivation,
  canReorderHeroSmsActivation,
  getHeroSmsStatusPresentation,
  isHeroSmsActiveStatus,
} from './status-meta'

const identityT = (value: string) => value
const activation = (status: string) => ({
  id: '1',
  order_id: '1',
  domain_id: 'quote-1',
  email: 'a',
  status,
  charge_quota: 1,
  cost_usd: 1,
  currency: 'USD',
  currency_code: 840,
  cancel_reason: '',
  created_at: '',
  updated_at: '',
})

describe('email activation status helpers', () => {
  test('polls only while active items remain', () => {
    assert.equal(
      getHeroSmsPollingInterval([activation('active')], true, true),
      5000
    )
    assert.equal(
      getHeroSmsPollingInterval([activation('active')], false, true),
      30000
    )
    assert.equal(
      getHeroSmsPollingInterval([activation('cancelled')], true, true),
      false
    )
  })

  test('maps statuses to actionable capabilities', () => {
    assert.equal(isHeroSmsActiveStatus('active'), true)
    assert.equal(canCancelHeroSmsActivation('active'), true)
    assert.equal(canReorderHeroSmsActivation('active'), false)
    assert.equal(canReorderHeroSmsActivation('completed'), true)

    const status = getHeroSmsStatusPresentation(
      'cancel_pending',
      identityT as never
    )
    assert.equal(status.label, 'Cancel pending')
    assert.equal(status.tone, 'warning')
  })
})
