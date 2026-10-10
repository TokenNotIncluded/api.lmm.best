/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createInstance } from 'i18next'
import type { ReactElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'

import { SmsBalanceNotice } from '@/features/email-activations/sms-balance-notice'
import { BountyProgress } from '@/features/open-source-bounties/bounty-progress'
import type { BountyChallenge } from '@/features/open-source-bounties/types'
import { useAuthStore } from '@/stores/auth-store'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'
import { useWalletCurrencyPreferenceStore } from '@/stores/wallet-currency-preference-store'

function renderSmsCurrencyFixture(element: ReactElement) {
  const originalUser = useAuthStore.getState().auth.user
  const originalCurrency = useSystemConfigStore.getState().config.currency
  const originalPreference =
    useWalletCurrencyPreferenceStore.getState().preference
  const authState = useAuthStore.getInitialState()
  const configState = useSystemConfigStore.getInitialState()
  const preferenceState = useWalletCurrencyPreferenceStore.getInitialState()
  const authSnapshot = authState.auth
  const configSnapshot = configState.config
  const preferenceSnapshot = preferenceState.preference
  try {
    useAuthStore.getState().auth.setUser(null)
    useWalletCurrencyPreferenceStore.getState().setPreference('')
    useSystemConfigStore.getState().setConfig({
      currency: {
        ...DEFAULT_CURRENCY_CONFIG,
        currencyUnit: 'credit',
        creditsPerUsd: 500_000,
        creditsPerUsdExact: '500000',
        cnyPerUsd: 7,
        cnyPerUsdExact: '7',
        legacyPricingUnitsPerUsd: 1,
        quotaPerUnit: 500_000,
      },
    })
    // SSR reads the initial Zustand snapshot, so supply the same complete
    // denomination fixture that the mounted application reads from live state.
    authState.auth = useAuthStore.getState().auth
    configState.config = useSystemConfigStore.getState().config
    preferenceState.preference =
      useWalletCurrencyPreferenceStore.getState().preference
    return renderToStaticMarkup(element)
  } finally {
    authState.auth = authSnapshot
    configState.config = configSnapshot
    preferenceState.preference = preferenceSnapshot
    useAuthStore.getState().auth.setUser(originalUser)
    useSystemConfigStore.getState().setConfig({ currency: originalCurrency })
    useWalletCurrencyPreferenceStore
      .getState()
      .setPreference(originalPreference)
  }
}

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
    const html = renderSmsCurrencyFixture(
      <I18nextProvider i18n={i18n}>
        <SmsBalanceNotice
          status='below-minimum'
          balanceQuota={625_000}
          isLoading={false}
          isRefreshing={false}
          onRefresh={() => {}}
        />
      </I18nextProvider>
    )
    const currency = language === 'zhCN' || language === 'zhTW' ? 'CNY' : 'USD'
    const balance = new Intl.NumberFormat(locale, {
      minimumFractionDigits: 0,
      maximumFractionDigits: 8,
    }).format(currency === 'CNY' ? 8.75 : 625_000 / 500_000)
    assert.ok(html.includes(`Current balance: ${balance} ${currency}`))
    assert.ok(!html.includes('{{balance}}'))
    assert.ok(html.includes('Refresh balance'))
  })
}
