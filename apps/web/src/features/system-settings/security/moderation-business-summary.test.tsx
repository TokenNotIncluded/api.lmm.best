/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import { ModerationBusinessSummary } from './moderation-business-summary'
import {
  recordedCount,
  recordedCredits,
  summarizePageAppeals,
} from './moderation-recorded-values'
import type {
  ModerationQueueStats,
  ModerationReview,
  ModerationAppeal,
} from './security-audit-types'

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

test('unknown or unsafe amounts remain unavailable while explicit zero is displayed', () => {
  for (const value of [
    undefined,
    null,
    Number.NaN,
    Infinity,
    -1,
    1.5,
    9007199254740992,
  ]) {
    assert.equal(recordedCredits(value), undefined)
    assert.equal(recordedCount(value), undefined)
  }
  assert.equal(recordedCredits(0, 'en'), '0 CREDIT')
  assert.equal(recordedCredits(500000, 'en'), '500,000 CREDIT')
})

test('Chinese interface language codes render recorded credits without Intl errors', async () => {
  for (const locale of ['zhCN', 'zhTW']) {
    assert.equal(recordedCredits(0, locale), '0 CREDIT')
    assert.equal(recordedCredits(500000, locale), '500,000 CREDIT')
    assert.equal(recordedCredits(Number.NaN, locale), undefined)
    const instance = createInstance()
    await instance.use(initReactI18next).init({
      lng: locale,
      resources: { [locale]: { translation: {} } },
    })
    const html = renderToStaticMarkup(
      <I18nextProvider i18n={instance}>
        <ModerationBusinessSummary
          stats={
            { completed: 0, charged_quota: 500000 } as ModerationQueueStats
          }
        />
      </I18nextProvider>
    )
    assert.match(html, /500,000 CREDIT/)
    assert.match(html, />0</)
  }
})

test('appeal counts are associated only with fee records on the current review page', () => {
  const reviews = [
    { fee_record_id: 7 },
    { fee_record_id: 7 },
    { fee_record_id: 8 },
    { fee_record_id: 0 },
  ] as ModerationReview[]
  assert.deepEqual(
    summarizePageAppeals(reviews, [
      { id: 1, record_id: 7, status: 'pending' },
      { id: 2, record_id: 8, status: 'approved' },
      { id: 3, record_id: 99, status: 'rejected' },
      { id: 4, record_id: 0, status: 'pending' },
    ]),
    { pending: 1, approved: 1, rejected: 0 }
  )
})

test('summary distinguishes original gross records, page-limited appeals and missing statistics', () => {
  const html = renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <ModerationBusinessSummary
        stats={{ completed: 0, charged_quota: 500000 } as ModerationQueueStats}
      />
    </I18nextProvider>
  )
  assert.match(html, /500,000 CREDIT/)
  assert.match(html, /No data provided/)
  assert.match(html, /reversed or refunded records/)
  assert.match(html, /not net wallet spending/)
  assert.match(html, /latest 200 records linked to reviews on this page/)
  assert.doesNotMatch(html, /USD|CNY|NaN|Infinity/)
})

test('an unavailable or malformed appeal response stays unknown rather than crashing or inventing zero', () => {
  const html = renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <ModerationBusinessSummary
        reviews={[]}
        appeals={{} as ModerationAppeal[]}
      />
    </I18nextProvider>
  )
  assert.match(html, /No data provided/)
  assert.doesNotMatch(html, />0</)
})
