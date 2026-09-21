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

import { customAcquisitionRange, presetAcquisitionRange } from './report-range'

const now = Date.parse('2026-09-19T14:00:00Z') / 1000

test('today begins at UTC midnight and custom end date is inclusive', () => {
  assert.deepEqual(presetAcquisitionRange(0, now), {
    from: Date.parse('2026-09-19T00:00:00Z') / 1000,
    to: now,
  })
  assert.deepEqual(customAcquisitionRange('2026-09-01', '2026-09-18', now), {
    from: Date.parse('2026-09-01T00:00:00Z') / 1000,
    to: Date.parse('2026-09-19T00:00:00Z') / 1000,
  })
  assert.equal(customAcquisitionRange('2026-09-01', '2026-09-19', now)?.to, now)
})
test('invalid, reversed, future and oversized date windows never become a query', () => {
  for (const [start, end] of [
    ['2026-02-30', '2026-03-02'],
    ['2026-09-18', '2026-09-01'],
    ['2026-09-01', '2026-09-20'],
    ['2024-01-01', '2026-09-19'],
    ['', ''],
  ]) {
    assert.equal(customAcquisitionRange(start, end, now), null)
  }
})
