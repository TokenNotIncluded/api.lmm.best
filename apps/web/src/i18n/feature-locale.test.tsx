/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'

import { SmsBalanceNotice } from '@/features/email-activations/sms-balance-notice'
import { BountyProgress } from '@/features/open-source-bounties/bounty-progress'
import type { BountyChallenge } from '@/features/open-source-bounties/types'

const challenge: BountyChallenge = {
  id: 1,
  project_id: 1,
  participant_user_id: 1,
  github_handle: 'example',
  status: 'accepted',
  issue_url: '',
  pull_request_url: '',
  submission_note: '',
  review_note: '',
  reward_quota: 0,
  tip_quota: 0,
  owner_rating_score: 0,
  owner_rating_comment: '',
  owner_rated_at: 0,
  contributor_rating_score: 0,
  contributor_rating_comment: '',
  contributor_rated_at: 0,
  accepted_at: 1_790_000_000,
  submitted_at: 0,
  reviewed_at: 0,
  paid_at: 0,
}

for (const [language, locale] of [
  ['zhCN', 'zh-CN'],
  ['zhTW', 'zh-TW'],
  ['en', 'en'],
  ['ja', 'ja'],
  ['invalid_locale', undefined],
] as const) {
  test(`bounty timeline renders a timestamp in ${language}`, async () => {
    const i18n = createInstance()
    await i18n.init({ lng: language, fallbackLng: false, resources: {} })
    const html = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <BountyProgress challenge={challenge} />
      </I18nextProvider>
    )
    assert.ok(
      html.includes(
        new Date(challenge.accepted_at * 1000).toLocaleString(locale)
      )
    )
    assert.ok(
      html.includes(new Date(challenge.accepted_at * 1000).toISOString())
    )
  })
  test(`SMS balance notice renders the supplied balance in ${language}`, async () => {
    const i18n = createInstance()
    await i18n.init({
      lng: language,
      fallbackLng: false,
      resources: {},
      interpolation: { escapeValue: false },
    })
    const html = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <SmsBalanceNotice
          status='below-minimum'
          balanceUSD={1.25}
          isLoading={false}
          isRefreshing={false}
          onRefresh={() => {}}
        />
      </I18nextProvider>
    )
    const balance = new Intl.NumberFormat(locale, {
      minimumFractionDigits: 2,
      maximumFractionDigits: 6,
    }).format(1.25)
    assert.ok(html.includes(`Current balance: USD ${balance}`))
    assert.ok(html.includes('Refresh balance'))
  })
}
