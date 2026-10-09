/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  buildOverviewUsage,
  expandOverviewGreeting,
  overviewLanguage,
  overviewUsageRange,
  parseOverviewPreference,
  validGreetingTemplate,
} from './overview-personalization-data'

test('greeting uses literal one-pass variables and locale fallback', () => {
  assert.equal(
    expandOverviewGreeting('HI,$name, $time; $$ $balance', {
      $name: '$time',
      $time: '12:30',
      $balance: '0.25',
    }),
    'HI,$time, 12:30; $ 0.25'
  )
  assert.ok(validGreetingTemplate('HI,$name,现在是$time'))
  assert.ok(validGreetingTemplate(''))
  assert.equal(validGreetingTemplate('$secret'), false)
  assert.equal(validGreetingTemplate('a'.repeat(513)), false)
  assert.equal(overviewLanguage('zh-Hant-TW'), 'zh-TW')
  assert.equal(overviewLanguage('zhCN'), 'zh')
  assert.equal(overviewLanguage('zhTW'), 'zh-TW')
  assert.equal(overviewLanguage('en-US'), 'en')
  assert.equal(overviewLanguage('xx'), 'en')
  assert.throws(() =>
    parseOverviewPreference({
      revision: 0,
      updated_at: 0,
      templates: { en: '$secret' },
    })
  )
  assert.throws(() =>
    parseOverviewPreference({ revision: -1, updated_at: 0, templates: {} })
  )
})
test('usage totals use recorded values and fold the remainder without loss', () => {
  const now = new Date(2026, 9, 9, 19, 0, 0)
  const range = overviewUsageRange(7, now)
  const rows = Array.from({ length: 7 }, (_, i) => ({
    created_at: range.end_timestamp,
    model_name: `model-${i}`,
    count: i + 1,
    token_used: (i + 1) * 10,
    quota: (i + 1) * 100,
  }))
  const data = buildOverviewUsage(
    rows,
    'requests',
    range.start_timestamp,
    range.end_timestamp,
    'en',
    'Other',
    'Unknown'
  )
  assert.equal(data.labels.length, 7)
  assert.equal(data.total, 28)
  assert.equal(data.models.length, 6)
  assert.equal(
    data.models.reduce((total, row) => total + row[1], 0),
    28
  )
  assert.equal(
    buildOverviewUsage(
      [],
      'tokens',
      range.start_timestamp,
      range.end_timestamp,
      'en',
      'Other',
      'Unknown'
    ).total,
    0
  )
  assert.throws(() =>
    buildOverviewUsage(
      [{ created_at: range.end_timestamp }],
      'tokens',
      range.start_timestamp,
      range.end_timestamp,
      'en',
      'Other',
      'Unknown'
    )
  )
})

test('usage date formatting accepts the actual Chinese interface codes', () => {
  const range = overviewUsageRange(7, new Date(2026, 9, 9, 19, 0, 0))
  for (const locale of ['zhCN', 'zhTW']) {
    const result = buildOverviewUsage(
      [{ created_at: range.end_timestamp, count: 42, model_name: 'test' }],
      'requests',
      range.start_timestamp,
      range.end_timestamp,
      locale,
      '其他',
      '未知'
    )
    assert.equal(result.total, 42)
    assert.equal(result.labels.length, 7)
    assert.ok(result.labels.some((label) => label.includes('月')))
  }
})
