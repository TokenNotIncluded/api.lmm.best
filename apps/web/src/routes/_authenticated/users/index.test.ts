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

import { usersSearchSchema } from './index'

describe('user management search state', () => {
  test('preserves sorting and activity filters across URL round trips', () => {
    const state = {
      sortBy: 'risk_score',
      sortOrder: 'desc',
      risk: 'high',
      transfers: 'sent',
      usage: 'zero',
      funding: 'unpaid',
      checkin: 'yes',
      page: 2,
    }
    const parsed = usersSearchSchema.parse(state)
    assert.equal(parsed.sortBy, 'risk_score')
    assert.equal(parsed.transfers, 'sent')
    assert.deepEqual(
      usersSearchSchema.parse(JSON.parse(JSON.stringify(parsed))),
      parsed
    )
    assert.equal(
      usersSearchSchema.parse({ sortBy: 'invalid', risk: 'invalid' }).risk,
      'all'
    )
  })
  test('shows all trust levels by default', () => {
    assert.equal(usersSearchSchema.parse({}).l0Only, false)
  })

  test('preserves an explicitly disabled L0-only filter', () => {
    assert.equal(usersSearchSchema.parse({ l0Only: false }).l0Only, false)
  })
})
