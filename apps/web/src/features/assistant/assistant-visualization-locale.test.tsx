/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import { formatHistoricalRawQuota } from '@/lib/historical-quota-money'

import { AssistantVisualizationCard } from './assistant-visualization'

test('native charts render with the real Chinese interface locale codes', async () => {
  const i18n = createInstance()
  await i18n.use(initReactI18next).init({
    lng: 'zhCN',
    fallbackLng: 'en',
    resources: {
      zhCN: { translation: {} },
      zhTW: { translation: {} },
      en: { translation: {} },
    },
  })
  for (const language of ['zhCN', 'zhTW']) {
    await i18n.changeLanguage(language)
    for (const chart_type of ['line', 'donut'] as const) {
      const markup = renderToStaticMarkup(
        <I18nextProvider i18n={i18n}>
          <AssistantVisualizationCard
            visual={{
              kind: 'chart',
              chart_type,
              title: '用量',
              labels: ['A', 'B'],
              series: [{ name: '请求', values: [1, 2] }],
            }}
          />
        </I18nextProvider>
      )
      assert.match(markup, /用量/)
      assert.match(markup, /<svg/)
      assert.doesNotMatch(markup, /NaN|Infinity/)
    }
  }
})

test('raw quota charts preserve every integer digit in tooltips, totals and data tables', async () => {
  const i18n = createInstance()
  await i18n.use(initReactI18next).init({
    lng: 'en',
    resources: { en: { translation: {} } },
  })
  for (const chart_type of ['line', 'bar', 'donut'] as const) {
    const markup = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <AssistantVisualizationCard
          formatChartValue={(value) =>
            formatHistoricalRawQuota(value, '', 'en').trimEnd()
          }
          visual={{
            kind: 'chart',
            chart_type,
            title: 'Raw quota history',
            labels: ['A', 'B'],
            series: [{ name: 'Raw credits', values: [3_500_001, 1] }],
            unit: 'Raw credits',
          }}
        />
      </I18nextProvider>
    )
    assert.match(markup, /<td>3,500,001 Raw credits<\/td>/)
    assert.match(markup, /<td>1 Raw credits<\/td>/)
    assert.match(markup, /<title>A[^<]*: 3,500,001<\/title>/)
    if (chart_type === 'donut') {
      assert.match(markup, />3,500,002<\/text>/)
      assert.match(markup, /<strong>3,500,001<\/strong>/)
    }
  }
  const defaultMarkup = renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <AssistantVisualizationCard
        visual={{
          kind: 'chart',
          chart_type: 'line',
          title: 'Default chart',
          labels: ['A'],
          series: [{ name: 'Value', values: [3_500_001] }],
        }}
      />
    </I18nextProvider>
  )
  assert.match(defaultMarkup, /<td>3,500,000<\/td>/)
})
