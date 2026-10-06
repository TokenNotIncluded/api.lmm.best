/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import { MarketAIReviewResultView, type MarketAIReviewResult } from './result'

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'zh',
  resources: { zh: { translation: { 'Sexual content': '色情内容' } } },
})
function render(result?: MarketAIReviewResult) {
  return renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <MarketAIReviewResultView result={result} />
    </I18nextProvider>
  )
}
test('assist result remains a suggestion and reuses translated categories', () => {
  const html = render({
    status: 'succeeded',
    decision: 'reject',
    applied: false,
    categories: ['sexual'],
  })
  assert.match(html, /AI suggests rejection/)
  assert.match(html, /AI decision not applied/)
  assert.match(html, /色情内容/)
  assert.match(html, /does not verify product quality/)
  assert.doesNotMatch(html, /AI decision applied/)
})
test('failed and unknown states do not imply a clean content check', () => {
  const failed = render({
    status: 'failed',
    applied: false,
    categories: [],
    error: '<provider unavailable>',
  })
  assert.match(failed, /awaiting human review/)
  assert.match(failed, /&lt;provider unavailable&gt;/)
  assert.doesNotMatch(failed, /No content categories flagged/)
  const unknown = render({
    status: 'constructor',
    applied: false,
    categories: [],
  })
  assert.match(unknown, /Unknown AI review state/)
  assert.equal(render(), '')
  const unknownCategories = render({
    status: 'succeeded',
    applied: false,
    categories: [],
    categoriesKnown: false,
  })
  assert.doesNotMatch(unknownCategories, /No content categories flagged/)
})

test('recorded check dates use the selected UI language including zhTW alias', async () => {
  const original = Date.prototype.toLocaleString
  const locales: unknown[] = []
  Date.prototype.toLocaleString = function (locale?: Intl.LocalesArgument) {
    locales.push(locale)
    return 'Recorded fixture date'
  }
  try {
    for (const language of ['zhTW', 'fr']) {
      await i18n.changeLanguage(language)
      assert.match(
        render({
          status: 'succeeded',
          applied: false,
          categories: [],
          checkedAt: 1791288000,
        }),
        /Recorded fixture date/
      )
    }
    assert.deepEqual(locales, ['zh-TW', 'fr'])
  } finally {
    Date.prototype.toLocaleString = original
    await i18n.changeLanguage('zh')
  }
})
