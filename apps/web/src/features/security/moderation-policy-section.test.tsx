/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import assert from 'node:assert/strict'
import { after, beforeEach, describe, test } from 'node:test'

import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider, initReactI18next } from 'react-i18next'

import appI18n from '@/i18n/config'
import { useSystemConfigStore } from '@/stores/system-config-store'
import { useWalletCurrencyPreferenceStore } from '@/stores/wallet-currency-preference-store'

import { ModerationPolicySection } from './moderation-policy-section'
import type { SecurityModerationPolicy } from './types'

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

const originalConfig = useSystemConfigStore.getState().config
const originalPreference =
  useWalletCurrencyPreferenceStore.getState().preference
const originalLanguage = appI18n.language
beforeEach(async () => {
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...originalConfig.currency,
      currencyUnit: 'credit',
      creditsPerUsd: 3_600_000,
      creditsPerUsdExact: '3600000',
      cnyPerUsd: 7.2,
      cnyPerUsdExact: '7.2',
      legacyPricingUnitsPerUsd: 14,
    },
  })
  useWalletCurrencyPreferenceStore.getState().setPreference('USD')
  await appI18n.changeLanguage('en')
})
after(async () => {
  useSystemConfigStore.getState().setConfig(originalConfig)
  useWalletCurrencyPreferenceStore.getState().setPreference(originalPreference)
  await appI18n.changeLanguage(originalLanguage)
})

function render(policy?: SecurityModerationPolicy) {
  // Static rendering reads Zustand's server snapshot; install this test's
  // explicit denomination and preference without changing the app defaults.
  const configSnapshot = useSystemConfigStore.getInitialState()
  const preferenceSnapshot = useWalletCurrencyPreferenceStore.getInitialState()
  const config = configSnapshot.config
  const preference = preferenceSnapshot.preference
  configSnapshot.config = useSystemConfigStore.getState().config
  preferenceSnapshot.preference =
    useWalletCurrencyPreferenceStore.getState().preference
  try {
    return renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <ModerationPolicySection policy={policy} isLoading={false} />
      </I18nextProvider>
    )
  } finally {
    configSnapshot.config = config
    preferenceSnapshot.preference = preference
  }
}

function policy(
  overrides: Partial<SecurityModerationPolicy> = {}
): SecurityModerationPolicy {
  return {
    enabled: false,
    assistant_enabled: false,
    engine: 'openai_moderation',
    async: true,
    group_policies: {},
    supported_inputs: ['text'],
    notice_only: true,
    ...overrides,
  }
}

describe('public Moderation policy', () => {
  test('does not invent active groups or prices when configuration is unavailable', () => {
    const html = render()
    assert.match(html, /Moderation settings are not published yet/)
    assert.match(html, /Active groups and fees cannot be confirmed/)
    assert.doesNotMatch(html, /Current Moderation settings/)
    assert.doesNotMatch(html, /Moderation group policies/)
    assert.match(html, /href="\/legal\/safety-review\.html"/)
  })

  test('shows disabled defaults without claiming that configured groups are active', () => {
    const html = render(
      policy({
        group_policies: {
          existing: {
            mode: 'strict',
            amount_currency: 'USD',
            category_fines_usd: { hate: 0.015 },
          },
        },
      })
    )
    assert.equal((html.match(/>Disabled</g) ?? []).length, 2)
    assert.match(html, /A disabled feature does not review requests/)
    assert.match(html, /existing/)
    assert.match(html, /Strict mode/)
    assert.match(html, /0\.015 USD/)
    assert.match(html, /Features and groups are disabled by default/)
  })

  test('publishes distinct group modes and strict fees with their enforcement limits', () => {
    const html = render(
      policy({
        enabled: true,
        assistant_enabled: true,
        group_policies: {
          exempt: { mode: 'off', category_fines_usd: { hate: 99 } },
          warn: { mode: 'tolerant', category_fines_usd: { hate: 98 } },
          paid: {
            mode: 'strict',
            amount_currency: 'USD',
            category_fines_usd: { hate: 0.25, violence: 0.5 },
          },
        },
      })
    )
    assert.match(html, /Moderation is disabled for this group/)
    assert.match(html, /without a wallet deduction/)
    assert.match(html, /0\.25 USD/)
    assert.match(html, /0\.5 USD/)
    assert.doesNotMatch(html, /99 USD|98 USD/)
    assert.match(html, /largest configured fee among matched categories once/)
    assert.match(html, /reviews never overdraw the wallet/)
    assert.match(
      html,
      /Deductions round down to the wallet’s smallest supported amount/
    )
    assert.match(html, /Smaller fees only trigger a warning/)
    assert.match(html, /Requests do not wait for the review verdict/)
    assert.match(html, /Model output violations do not charge the user/)
    assert.match(html, /risk score/)
    assert.match(html, /text only/)
  })

  test('keeps zero fees explicit and excludes invalid unpublished amounts', () => {
    const html = render(
      policy({
        group_policies: {
          zero: {
            mode: 'strict',
            amount_currency: 'USD',
            category_fines_usd: {},
          },
          invalid: {
            mode: 'strict',
            amount_currency: 'USD',
            category_fines_usd: {
              harassment: 0,
              hate: Number.NaN,
              violence: -2,
            },
          },
        },
      })
    )
    assert.match(
      html,
      /No category fees are configured; the review fee is zero\./
    )
    assert.match(html, /harassment/)
    assert.match(html, /0 USD/)
    assert.doesNotMatch(html, />hate<|>violence<|NaN|-2 USD/)
  })
})

test('public category fees remain real USD independently of legacy calibration', () => {
  const published = policy({
    group_policies: {
      strict: {
        mode: 'strict',
        amount_currency: 'USD',
        category_fines_usd: { hate: 0.25 },
      },
    },
  })
  assert.match(render(published), /0\.25 USD/)
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...useSystemConfigStore.getState().config.currency,
      legacyPricingUnitsPerUsd: 140,
    },
  })
  assert.match(render(published), /0\.25 USD/)
  useWalletCurrencyPreferenceStore.getState().setPreference('CNY')
  assert.match(render(published), /1\.8 CNY/)
  useWalletCurrencyPreferenceStore.getState().setPreference('CREDIT')
  assert.match(render(published), /900,000 Credits/)
})
