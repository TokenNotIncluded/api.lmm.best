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
