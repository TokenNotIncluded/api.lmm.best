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

import { csvCell, sourceSummaryCSV } from './summary-export'

test('summary export preserves unknown metrics and neutralizes spreadsheet formulas', () => {
  assert.equal(csvCell('=HYPERLINK("x")'), '"\'=HYPERLINK(""x"")"')
  assert.equal(csvCell(-7), '"-7"')
  const csv = sourceSummaryCSV({
    from: 1,
    to: 2,
    observed_until: 3,
    channels: [
      {
        source: '@source',
        evidence: 'promotion_link',
        registrations: 1,
        identified_registrations: 1,
      },
    ],
    payments: [],
    activity_state: {
      started_at: 1,
      scanned_through: 2,
      status: 'rebuilding',
      incomplete: true,
    },
    activity: [
      {
        source: '@source',
        eligible_accounts: 1,
        successful_accounts: 1,
        mature_accounts: 0,
        retained_accounts: 0,
        observing_accounts: 1,
        retention_rate: null,
      },
    ],
  })
  assert.ok(csv.includes('"retention_rate","unknown"'))
  assert.ok(csv.includes('"\'@source"'))
  assert.ok(csv.includes('"activity_processed_through","2"'))
  assert.equal(csv.includes('user_id'), false)
  assert.equal(csv.includes('email'), false)
})
