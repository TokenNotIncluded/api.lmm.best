/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'

import { Window } from 'happy-dom'
import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'

import { useSystemConfigStore } from '@/stores/system-config-store'

import type { QuotaDataItem } from '../../types'
import { ModelUsageTable } from './model-usage-table'

const originalConfig = useSystemConfigStore.getState().config
const i18n = createInstance()
await i18n.init({
  lng: 'en',
  fallbackLng: 'en',
  resources: { en: { translation: { credits: 'Credits' } } },
})
afterEach(() => useSystemConfigStore.setState({ config: originalConfig }))

function tableText(data: QuotaDataItem[]) {
  const window = new Window()
  try {
    window.document.body.innerHTML = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <ModelUsageTable data={data} />
      </I18nextProvider>
    )
    const table = window.document.querySelector('table')
    assert.ok(table)
    return {
      text: table.textContent ?? '',
      cells: [...table.querySelectorAll('tbody td')].map(
        (cell) => cell.textContent
      ),
    }
  } finally {
    window.close()
  }
}

test('model usage shows raw quota independently of current wallet FX', () => {
  for (const cnyPerUsd of [6.7, 7.5]) {
    useSystemConfigStore.setState({
      config: {
        ...originalConfig,
        currency: { ...originalConfig.currency, cnyPerUsd },
      },
    })
    const { text, cells } = tableText([
      {
        created_at: 0,
        model_name: 'legacy',
        count: 2,
        token_used: 200,
        quota: 3_500_000,
        quota_money_available: false,
        quota_money_basis: 'bucket_without_credit_basis',
      },
    ])
    assert.deepEqual(cells, ['2', '200', '3,500,000 Raw credits', '100%'])
    assert.match(text, /Raw quota/)
    assert.match(text, /Quota share/)
    assert.match(text, /historical money basis is unavailable/)
    assert.doesNotMatch(text, /USD|CNY|Cost share/)
  }
})

test('model table preserves exact aggregate integers and leaves unsafe sources unknown', () => {
  const exact = tableText([
    { created_at: 0, model_name: 'large', quota: Number.MAX_SAFE_INTEGER },
    { created_at: 1, model_name: 'large', quota: 2 },
  ])
  assert.equal(exact.cells[2], '9,007,199,254,740,993 Raw credits')
  const unknown = tableText([
    { created_at: 0, model_name: 'unsafe', quota: Number.MAX_SAFE_INTEGER + 1 },
  ])
  assert.equal(unknown.cells[2], '—')
  assert.equal(unknown.cells[3], '—')
})

test('unknown raw quota does not become zero percent or give another model one hundred percent', () => {
  for (const quota of [Number.NaN, null as unknown as number, undefined]) {
    const unknown = tableText([
      { created_at: 0, model_name: 'unknown', quota },
      { created_at: 0, model_name: 'known', quota: 1 },
    ])
    assert.equal(unknown.cells[3], '—')
    assert.equal(unknown.cells[7], '—')
    assert.doesNotMatch(unknown.text, /0%|100%/)
  }
  const realZero = tableText([
    { created_at: 0, model_name: 'zero', quota: 0 },
    { created_at: 0, model_name: 'known', quota: 1 },
  ])
  assert.match(realZero.text, /0 Raw credits/)
  assert.match(realZero.text, /0%/)
  assert.match(realZero.text, /100%/)
})
