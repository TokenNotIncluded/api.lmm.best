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
  ])
    {assert.equal(customAcquisitionRange(start, end, now), null)}
})
