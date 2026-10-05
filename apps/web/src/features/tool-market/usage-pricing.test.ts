/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  maximumUsageQuota,
  usageMetrics,
  usageSourceLabel,
} from './usage-pricing'

test('resource price holds sum CPU and memory limits using exact integer rounding', () => {
  assert.equal(
    maximumUsageQuota([
      { metric: 'cpu_core_milliseconds', rate_quota: 100, max_quantity: 1000 },
      { metric: 'memory_mib_seconds', rate_quota: 200, max_quantity: 1024 },
    ]),
    300
  )
  assert.equal(
    maximumUsageQuota([
      { metric: 'input_tokens', rate_quota: 1470000, max_quantity: 65536 },
    ]),
    96338
  )
  assert.equal(
    maximumUsageQuota([{ metric: 'images', rate_quota: 1, max_quantity: 1 }]),
    1
  )
  for (const m of usageMetrics) {
    assert.equal(
      maximumUsageQuota([
        { metric: m.metric, rate_quota: 7, max_quantity: m.scale * 2 },
      ]),
      14
    )
  }
})
test('invalid and overflowing rule sets cannot produce a displayed price', () => {
  for (const rules of [
    [],
    [{ metric: 'unknown', rate_quota: 1, max_quantity: 1 }],
    [{ metric: 'images', rate_quota: 0, max_quantity: 1 }],
    [{ metric: 'images', rate_quota: 1, max_quantity: -1 }],
    [{ metric: 'images', rate_quota: 1, max_quantity: 1.5 }],
    [
      { metric: 'images', rate_quota: 1, max_quantity: 1 },
      { metric: 'images', rate_quota: 2, max_quantity: 1 },
    ],
    [
      {
        metric: 'images',
        rate_quota: Number.MAX_SAFE_INTEGER,
        max_quantity: 1e12,
      },
    ],
  ]) {
    assert.throws(() => maximumUsageQuota(rules))
  }
})

test('legacy and unknown usage sources are not asserted to be reported or independently verified', () => {
  assert.equal(usageSourceLabel(undefined), 'Usage source unavailable')
  assert.equal(usageSourceLabel(''), 'Usage source unavailable')
  assert.equal(usageSourceLabel('future_source'), 'Usage source unavailable')
  assert.equal(usageSourceLabel('tool_reported'), 'Tool-reported usage')
  assert.equal(usageSourceLabel('platform_verified'), 'Verified usage')
})
