/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  buildModelUsageQueryRanges,
  buildModelUsageReport,
  getModelUsageRange,
  MODEL_USAGE_YEAR_DAYS,
  MODEL_USAGE_WINDOW_SECONDS,
  mergeModelUsageRows,
  normalizeModelName,
  type ModelUsageRow,
} from './model-usage'

function ts(year: number, month: number, day: number, hour = 12): number {
  return Math.floor(new Date(year, month - 1, day, hour).getTime() / 1000)
}

function row(overrides: Partial<ModelUsageRow> = {}): ModelUsageRow {
  return {
    created_at: ts(2026, 3, 1),
    count: 1,
    token_used: 100,
    quota: 1,
    model_name: 'gpt-5',
    ...overrides,
  }
}

describe('normalizeModelName', () => {
  test('trims real names and buckets blanks under unknown', () => {
    assert.equal(normalizeModelName('  claude-sonnet-5 '), 'claude-sonnet-5')
    assert.equal(normalizeModelName(''), 'unknown')
    assert.equal(normalizeModelName('   '), 'unknown')
    assert.equal(normalizeModelName(null), 'unknown')
    assert.equal(normalizeModelName(undefined), 'unknown')
  })
})

describe('mergeModelUsageRows', () => {
  test('folds the same model across windows into one row', () => {
    const merged = mergeModelUsageRows([
      row({ model_name: 'gpt-5', count: 2, token_used: 200, quota: 5 }),
      row({ model_name: 'gpt-5', count: 3, token_used: 300, quota: 7 }),
    ])

    const model = merged.get('gpt-5')
    assert.ok(model)
    assert.equal(model.requests, 5)
    assert.equal(model.tokens, 500)
    assert.equal(model.quota, 12)
  })

  test('keeps distinct models apart and never drops an unnamed row', () => {
    const merged = mergeModelUsageRows([
      row({ model_name: 'gpt-5' }),
      row({ model_name: 'claude-sonnet-5' }),
      row({ model_name: '' }),
    ])

    assert.equal(merged.size, 3)
    assert.ok(merged.has('unknown'))
  })

  test('treats missing or negative counters as zero rather than NaN', () => {
    const merged = mergeModelUsageRows([
      row({ model_name: 'gpt-5', count: undefined, token_used: -5, quota: 4 }),
    ])

    const model = merged.get('gpt-5')
    assert.ok(model)
    assert.equal(model.requests, 0)
    assert.equal(model.tokens, 0)
    assert.equal(model.quota, 4)
  })
})

describe('buildModelUsageReport', () => {
  test('ranks by quota and normalizes intensity against the leader', () => {
    const report = buildModelUsageReport([
      row({ model_name: 'small', count: 1, token_used: 100, quota: 10 }),
      row({ model_name: 'big', count: 9, token_used: 900, quota: 90 }),
      row({ model_name: 'mid', count: 4, token_used: 400, quota: 40 }),
    ])

    assert.deepEqual(
      report.models.map((model) => model.modelName),
      ['big', 'mid', 'small']
    )
    // Intensity is relative to the largest model, share to the whole.
    assert.equal(report.models[0].intensity, 1)
    assert.equal(report.models[0].share, 90 / 140)
    assert.equal(report.models[1].intensity, 40 / 90)
    assert.equal(report.models[2].intensity, 10 / 90)
  })

  test('sums totals and exposes the top model', () => {
    const report = buildModelUsageReport([
      row({ model_name: 'a', count: 2, token_used: 20, quota: 3 }),
      row({ model_name: 'b', count: 5, token_used: 50, quota: 8 }),
    ])

    assert.equal(report.totals.requests, 7)
    assert.equal(report.totals.tokens, 70)
    assert.equal(report.totals.quota, 11)
    assert.equal(report.totals.modelCount, 2)
    assert.equal(report.totals.topModel?.modelName, 'b')
  })

  test('falls back to tokens when no window reports spend', () => {
    const report = buildModelUsageReport([
      row({ model_name: 'a', count: 1, token_used: 900, quota: 0 }),
      row({ model_name: 'b', count: 1, token_used: 300, quota: 0 }),
    ])

    assert.equal(report.models[0].modelName, 'a')
    assert.equal(report.models[0].intensity, 1)
    assert.equal(report.models[0].share, 0.75)
  })

  test('uses a single metric for paid and free models', () => {
    const report = buildModelUsageReport([
      row({ model_name: 'free', token_used: 9000, quota: 0 }),
      row({ model_name: 'paid', token_used: 10, quota: 5 }),
    ])
    assert.equal(report.shareMetric, 'quota')
    assert.equal(
      report.models.find((model) => model.modelName === 'free')?.share,
      0
    )
    assert.equal(report.totals.tokens, 9010)
  })

  test('retains request-only models and every model in the report', () => {
    const report = buildModelUsageReport(
      Array.from({ length: 15 }, (_, index) =>
        row({
          model_name: `model-${index}`,
          count: index + 1,
          token_used: 0,
          quota: 0,
        })
      )
    )
    assert.equal(report.models.length, 15)
    assert.equal(report.shareMetric, 'requests')
    assert.equal(report.models[0].modelName, 'model-14')
    assert.equal(
      report.models.reduce((sum, model) => sum + model.requests, 0),
      report.totals.requests
    )
  })

  test('breaks quota ties by tokens so live models outrank idle ones', () => {
    const report = buildModelUsageReport([
      row({ model_name: 'idle', count: 0, token_used: 0, quota: 5 }),
      row({ model_name: 'busy', count: 4, token_used: 800, quota: 5 }),
    ])

    assert.equal(report.models[0].modelName, 'busy')
  })

  test('returns an empty report instead of dividing by zero', () => {
    const report = buildModelUsageReport([])

    assert.deepEqual(report.models, [])
    assert.equal(report.totals.requests, 0)
    assert.equal(report.totals.tokens, 0)
    assert.equal(report.totals.modelCount, 0)
    assert.equal(report.totals.topModel, null)
  })
})

describe('getModelUsageRange', () => {
  const now = new Date(2026, 5, 30, 12)

  test('anchors every range on now', () => {
    assert.equal(
      getModelUsageRange('7d', now).end_timestamp,
      ts(2026, 6, 30, 12)
    )
    assert.equal(
      getModelUsageRange('30d', now).end_timestamp,
      ts(2026, 6, 30, 12)
    )
  })

  test('subtracts the right number of days per key', () => {
    assert.equal(
      getModelUsageRange('7d', now).start_timestamp,
      ts(2026, 6, 23, 12)
    )
    assert.equal(
      getModelUsageRange('30d', now).start_timestamp,
      ts(2026, 5, 31, 12)
    )
    assert.equal(
      getModelUsageRange('365d', now).start_timestamp,
      ts(2026, 6, 30, 12) - MODEL_USAGE_YEAR_DAYS * 24 * 60 * 60
    )
  })

  test('never reaches back before the account existed', () => {
    const created = ts(2026, 6, 28, 9)
    assert.equal(
      getModelUsageRange('365d', now, created).start_timestamp,
      created
    )
    // A newer-than-range anchor still leaves the window intact.
    assert.equal(
      getModelUsageRange('7d', now, ts(2020, 1, 1)).start_timestamp,
      ts(2026, 6, 23, 12)
    )
    // A zero/absent creation time must not clamp the range to the epoch.
    assert.equal(
      getModelUsageRange('365d', now, 0).start_timestamp,
      ts(2026, 6, 30, 12) - MODEL_USAGE_YEAR_DAYS * 24 * 60 * 60
    )
    assert.equal(
      getModelUsageRange('7d', now, ts(2027, 1, 1)).start_timestamp,
      ts(2026, 6, 30, 12)
    )
  })
})

describe('buildModelUsageQueryRanges', () => {
  test('splits a span the server would reject into compliant windows', () => {
    const start = ts(2026, 1, 1)
    const end = ts(2026, 12, 31)
    const ranges = buildModelUsageQueryRanges({
      start_timestamp: start,
      end_timestamp: end,
    })

    assert.ok(ranges.length > 1)
    for (const range of ranges) {
      const width = range.end_timestamp - range.start_timestamp
      assert.ok(
        width <= MODEL_USAGE_WINDOW_SECONDS,
        `window of ${width}s exceeds the server cap`
      )
    }
    assert.equal(ranges[0].start_timestamp, start)
    assert.equal(ranges.at(-1)?.end_timestamp, end)
  })

  test('leaves a short range as a single window', () => {
    const ranges = buildModelUsageQueryRanges({
      start_timestamp: ts(2026, 6, 23),
      end_timestamp: ts(2026, 6, 30),
    })

    assert.equal(ranges.length, 1)
  })
})
