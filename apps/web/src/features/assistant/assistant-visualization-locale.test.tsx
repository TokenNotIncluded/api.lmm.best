/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

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
