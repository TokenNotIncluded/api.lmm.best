/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'

import { DrawingGallery } from './drawing-gallery'
import type { DrawingPreview } from './drawing-history'

for (const language of ['zhCN', 'zhTW', 'en', 'fr', 'ja', 'ru', 'vi']) {
  test(`stored drawing renders its date in ${language}`, async () => {
    const i18n = createInstance()
    await i18n.init({
      lng: language,
      resources: { [language]: { translation: {} } },
      interpolation: { escapeValue: false },
    })
    const image: DrawingPreview = {
      id: 'locale-regression',
      userId: 1,
      createdAt: Date.UTC(2026, 8, 12),
      prompt: 'A blue circle',
      model: 'gpt-image-2',
      group: 'image-2',
      src: 'https://example.test/circle.png',
      saved: true,
    }
    const html = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <DrawingGallery images={[image]} />
      </I18nextProvider>
    )
    assert.match(html, /<img /)
    assert.match(
      html,
      /<time dateTime="2026-09-12T00:00:00.000Z">[^<]+<\/time>/
    )
  })
}
