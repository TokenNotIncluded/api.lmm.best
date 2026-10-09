/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  modelUsageCsv,
  selectModelUsage,
  summarizeModelUsage,
} from './model-usage'

const descending = { metric: 'quota', direction: 'descending' } as const
const data = [
  { created_at: 1, model_name: 'alpha', count: 2, token_used: 20, quota: 3 },
  { created_at: 2, model_name: 'alpha', count: 1, token_used: 10, quota: 1 },
  { created_at: 1, model_name: 'beta', count: 4, token_used: 80, quota: 6 },
]

test('combines time buckets and users without changing the input', () => {
  const input = structuredClone(data)
  assert.deepEqual(summarizeModelUsage(input), [
    { model: 'alpha', count: 3, tokens: 30, quota: 4, share: 0.4 },
    { model: 'beta', count: 4, tokens: 80, quota: 6, share: 0.6 },
  ])
  assert.deepEqual(input, data)
})

test('search does not change the share denominator or original order', () => {
  const rows = summarizeModelUsage(data)
  const selected = selectModelUsage(rows, '  ALPHA  ', descending, 'Unknown')
  assert.equal(selected.length, 1)
  assert.equal(selected[0].share, 0.4)
  assert.equal(rows[0].model, 'alpha')
  assert.equal(selectModelUsage(rows, '', descending, 'Unknown')[0].model, 'beta')
})

test('supports every numeric sort direction and deterministic ties', () => {
  const rows = summarizeModelUsage(data)
  for (const metric of ['count', 'tokens', 'quota'] as const) {
    assert.equal(
      selectModelUsage(rows, '', { metric, direction: 'ascending' }, '')[0].model,
      'alpha'
    )
    assert.equal(
      selectModelUsage(rows, '', { metric, direction: 'descending' }, '')[0].model,
      'beta'
    )
  }
  const ties = summarizeModelUsage([
    { created_at: 0, model_name: 'z', quota: 1 },
    { created_at: 0, model_name: 'a', quota: 1 },
  ])
  assert.deepEqual(selectModelUsage(ties, '', descending, '').map((r) => r.model), ['a', 'z'])
})

test('empty data and zero cost do not invent a percentage', () => {
  assert.deepEqual(summarizeModelUsage([]), [])
  const rows = summarizeModelUsage([{ created_at: 0, model_name: 'free', count: 3 }])
  assert.deepEqual(rows, [{ model: 'free', count: 3, tokens: 0, quota: 0, share: null }])
  assert.deepEqual(selectModelUsage(rows, 'not-found', descending, ''), [])
})

test('keeps unknown, literal Unknown, and object-like names separate', () => {
  const rows = summarizeModelUsage(
    ['', 'Unknown', '__proto__', 'constructor'].map((model_name) => ({
      created_at: 0, model_name, quota: 1,
    }))
  )
  assert.equal(rows.length, 4)
  assert.equal(selectModelUsage(rows, '未知', descending, '未知模型')[0].model, '')
})

test('does not leak invalid numbers and preserves signed adjustments', () => {
  const rows = summarizeModelUsage([
    { created_at: 0, count: NaN, token_used: Infinity, quota: -2 },
    { created_at: 1, quota: 5 },
  ])
  assert.deepEqual(rows, [{ model: '', count: 0, tokens: 0, quota: 3, share: 1 }])
})

test('CSV applies the supplied currency conversion and keeps full precision', () => {
  const csv = modelUsageCsv(summarizeModelUsage(data), ['Model', 'Calls', 'Tokens', 'CNY', '%'], (q) => q * 7.1, 'Unknown')
  assert.ok(csv.startsWith('\uFEFF"Model"'))
  assert.ok(csv.includes('"alpha","3","30","28.4","40"'))
  assert.ok(csv.endsWith('\r\n'))
})

test('CSV escapes quotes, line breaks, and formula-like model names', () => {
  for (const model of ['=1+1', '+1', '-cmd', '@SUM(1)', ' \t=1', '\r=1', '\uFEFF=1']) {
    const rows = summarizeModelUsage([{ created_at: 0, model_name: model }])
    assert.ok(modelUsageCsv(rows, [], (q) => q, '').includes(`"'${model}"`))
  }
  const rows = summarizeModelUsage([{ created_at: 0, model_name: 'a,"b\nc' }])
  assert.ok(modelUsageCsv(rows, [], (q) => q, '').includes('"a,""b\nc"'))
})

test('CSV includes the whole searched set, not only a visible page', () => {
  const rows = summarizeModelUsage(Array.from({ length: 35 }, (_, i) => ({
    created_at: 0, model_name: `model-${i}`, quota: i,
  })))
  const selected = selectModelUsage(rows, 'model-', descending, '')
  assert.equal(modelUsageCsv(selected, ['Model'], (q) => q, '').split('\r\n').length, 37)
})

test('feature translations cover all locales with matching placeholders', async () => {
  const { dashboardUsageTranslations } = await import('../model-usage-i18n')
  const english = dashboardUsageTranslations.en
  const keys = Object.keys(english).sort()
  const placeholders = (text: string) => [...text.matchAll(/{{(\w+)}}/g)].map((m) => m[1]).sort()
  assert.deepEqual(Object.keys(dashboardUsageTranslations).sort(), ['en', 'fr', 'ja', 'ru', 'vi', 'zhCN', 'zhTW'])
  for (const resources of Object.values(dashboardUsageTranslations)) {
    assert.deepEqual(Object.keys(resources).sort(), keys)
    for (const [key, value] of Object.entries(resources)) {
      assert.ok(value.trim())
      assert.deepEqual(placeholders(value), placeholders(english[key as keyof typeof english]))
    }
  }
})
