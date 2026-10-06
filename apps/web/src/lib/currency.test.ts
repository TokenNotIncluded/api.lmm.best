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

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { afterEach, beforeEach, test } from 'node:test'

import i18n from '@/i18n/config'
import { useAuthStore } from '@/stores/auth-store'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'
import { useWalletCurrencyPreferenceStore } from '@/stores/wallet-currency-preference-store'

import {
  displayAmountToQuota,
  formatCreditAmount,
  formatCurrencyFromUSD,
  formatFiatCurrencyAmount,
  formatPlatformAmount,
  formatQuotaWithCurrency,
  formatUSDInCurrency,
  getCurrencyLabel,
  legacyPlatformAmountToQuota,
  quotaToDisplayAmount,
  quotaToDisplayInput,
  resolveWalletDisplayCurrency,
} from './currency'

const exact = {
  locale: 'en-US',
  abbreviate: false,
  compact: false,
  creditLabel: 'Credits',
}
const original = useSystemConfigStore.getState().config
const oldPreference = useWalletCurrencyPreferenceStore.getState().preference
beforeEach(() => {
  useAuthStore.getState().auth.reset('idle')
  useWalletCurrencyPreferenceStore.getState().setPreference('USD')
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...DEFAULT_CURRENCY_CONFIG,
      currencyUnit: 'credit',
      quotaPerUnit: 500000,
      creditsPerUsd: 500000,
      creditsPerUsdExact: '500000',
      cnyPerUsd: 7.3,
      cnyPerUsdExact: '7.3',
      legacyPricingUnitsPerUsd: 7.3,
    },
  })
})
afterEach(() => {
  useSystemConfigStore.setState({ config: original })
  useWalletCurrencyPreferenceStore.getState().setPreference(oldPreference)
  useAuthStore.getState().auth.reset('idle')
})

test('language defaults and explicit unit priority', () => {
  assert.equal(resolveWalletDisplayCurrency('', 'zh-CN'), 'CNY')
  assert.equal(resolveWalletDisplayCurrency('', 'zhTW'), 'CNY')
  assert.equal(resolveWalletDisplayCurrency('', 'en'), 'USD')
  assert.equal(resolveWalletDisplayCurrency('CREDIT', 'zh-CN'), 'CREDIT')
  assert.equal(resolveWalletDisplayCurrency('USD', 'zh-CN'), 'USD')
})
test('raw Credits display fixed real USD and CNY with explicit labels', () => {
  assert.equal(formatQuotaWithCurrency(500000, exact), '1 USD')
  useWalletCurrencyPreferenceStore.getState().setPreference('CNY')
  assert.equal(formatQuotaWithCurrency(500000, exact), '7.3 CNY')
  useWalletCurrencyPreferenceStore.getState().setPreference('CREDIT')
  assert.equal(formatCreditAmount(3650000, exact), '3,650,000 Credits')
  assert.equal(getCurrencyLabel(), i18n.t('Credits'))
})
test('legacy batch bridge is distinct from true USD and existing fiat', () => {
  assert.equal(legacyPlatformAmountToQuota('7.3'), 3650000)
  assert.equal(formatPlatformAmount(7.3, exact), '7.3 USD')
  assert.equal(formatFiatCurrencyAmount(6.8, 'CNY', exact), '6.8 CNY')
  assert.equal(formatCurrencyFromUSD(7.3, exact), '7.3 USD')
})
test('fractional Credit model rates never floor to zero', () => {
  assert.equal(formatUSDInCurrency(0.0000001, 'CREDIT', exact), '0.05 Credits')
  assert.equal(formatUSDInCurrency(1, 'CNY', exact), '7.3 CNY')
})
test('recharge/display settings cannot revalue the fixed denomination', () => {
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...useSystemConfigStore.getState().config.currency,
      quotaDisplayType: 'CNY',
      usdExchangeRate: 99,
      legacyPricingUnitsPerUsd: 999,
    },
  })
  assert.equal(formatQuotaWithCurrency(500000, exact), '1 USD')
  assert.equal(quotaToDisplayAmount(500000, 'CNY'), 7.3)
})
test('invalid rates and unsafe amounts cannot fall back to 1:1', () => {
  for (const amount of [
    '',
    'NaN',
    'Infinity',
    Number.POSITIVE_INFINITY,
    '9007199254740992',
  ]) {
    assert.ok(Number.isNaN(displayAmountToQuota(amount, 'CREDIT')))
  }
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...useSystemConfigStore.getState().config.currency,
      creditsPerUsd: 0,
      cnyPerUsd: 0,
    },
  })
  assert.equal(formatQuotaWithCurrency(1, exact), '-')
  assert.equal(formatUSDInCurrency(1, 'CNY', exact), '-')
  assert.equal(formatUSDInCurrency(1, 'USD', exact), '1 USD')
  assert.ok(Number.isNaN(displayAmountToQuota('1', 'USD')))
  assert.ok(Number.isNaN(displayAmountToQuota('1', 'CREDIT')))
})
test('decimal writes floor exactly and preserve signed raw quota', () => {
  assert.equal(displayAmountToQuota('0.0000001', 'USD'), 0)
  assert.equal(displayAmountToQuota('0.1', 'USD'), 50000)
  assert.ok(Number.isNaN(displayAmountToQuota('1.9', 'CREDIT')))
  assert.ok(Number.isNaN(displayAmountToQuota('-1.1', 'CREDIT')))
  assert.equal(displayAmountToQuota('-1', 'CREDIT'), -1)
})
test('one Credit and max-safe balances survive exact editable round trips', () => {
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...useSystemConfigStore.getState().config.currency,
      creditsPerUsd: 500000,
      creditsPerUsdExact: '500000',
      cnyPerUsd: 6.730863,
      cnyPerUsdExact: '6.730863',
    },
  })
  for (const currency of ['USD', 'CNY', 'CREDIT'] as const) {
    for (const quota of [1, -1, 500000, Number.MAX_SAFE_INTEGER]) {
      const input = quotaToDisplayInput(quota, currency)
      assert.notEqual(input, '')
      assert.equal(displayAmountToQuota(input, currency), quota)
    }
  }
  assert.notEqual(formatQuotaWithCurrency(1, exact), '0 USD')
})
test('anonymous and account preferences are isolated', () => {
  useWalletCurrencyPreferenceStore.getState().setPreference('CREDIT')
  useAuthStore.getState().auth.setUser({
    id: 1,
    role: 1,
    username: 'a',
    setting: { wallet_display_currency: 'CNY' },
  })
  assert.equal(getCurrencyLabel(), 'CNY')
  useAuthStore.getState().auth.setUser({
    id: 2,
    role: 1,
    username: 'b',
    setting: { wallet_display_currency: 'USD' },
  })
  assert.equal(getCurrencyLabel(), 'USD')
  useAuthStore.getState().auth.reset('idle')
  assert.equal(getCurrencyLabel(), i18n.t('Credits'))
})

test('old exchange-rate denominations are rejected instead of revaluing Credits', () => {
  for (const creditsPerUsd of [3359744, 3650000, 100000]) {
    useSystemConfigStore.getState().setConfig({
      currency: {
        ...useSystemConfigStore.getState().config.currency,
        creditsPerUsd,
        creditsPerUsdExact: String(creditsPerUsd),
      },
    })
    assert.equal(formatQuotaWithCurrency(500000, exact), '-')
    assert.equal(formatCreditAmount(500000, exact), '-')
    assert.ok(Number.isNaN(displayAmountToQuota('1', 'USD')))
  }
})
