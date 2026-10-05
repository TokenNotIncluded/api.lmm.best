/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { afterEach, beforeEach, test } from 'node:test'

import {
  processChartData,
  processUserChartData,
} from '@/features/dashboard/lib/charts'
import { formatModelPrice } from '@/features/pricing/lib/price-display'
import { testKeyPayload, testKeyQuota } from '@/features/test-key/test-key'
import { marketQuota } from '@/features/tool-market/money'
import { mapStatusDataToConfig } from '@/hooks/use-system-config'
import i18n from '@/i18n/config'
import { useAuthStore } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'
import { useWalletCurrencyPreferenceStore } from '@/stores/wallet-currency-preference-store'

import {
  displayAmountToQuota,
  formatAmountInCurrency,
  formatCreditAmount,
  formatQuotaInCurrency,
  formatUSDInCurrency,
  getCurrencyDisplay,
  legacyPlatformAmountToQuota,
  quotaToDisplayAmount,
  quotaToDisplayInput,
  quotaToLegacyPlatformAmount,
} from './currency'
import {
  getEditableQuotaStep,
  parseQuotaFromDollars,
  quotaUnitsToDollars,
} from './format'

const initial = useSystemConfigStore.getState().config
const preference = useWalletCurrencyPreferenceStore.getState().preference
const initialAuth = useAuthStore.getState().auth
const v2 = {
  currency_unit: 'credit',
  credits_per_usd: 3359744,
  ledger_quota_per_usd: 3359744,
  ledger_quota_per_usd_exact: '3359744',
  public_credits_per_usd: 100000,
  public_credits_per_usd_exact: '100000',
  credit_unit_schema_version: 2,
  quota_unit: 'LEDGER_QUOTA',
  public_credit_unit: 'CREDIT',
  legacy_credit_unit: 'LEDGER_QUOTA',
  cny_per_usd: '6.719488',
  quota_per_unit: 500000,
}
const options = { locale: 'en-US', creditLabel: 'Credits', digitsLarge: 6 }
const currencyConfig = (
  data: Parameters<typeof mapStatusDataToConfig>[0] = v2
) => {
  const mapped = mapStatusDataToConfig(data).currency
  assert.ok(mapped)
  return getCurrencyDisplay(mapped).config
}
beforeEach(async () => {
  await i18n.changeLanguage('en')
  useAuthStore.getState().auth.reset('idle')
  useWalletCurrencyPreferenceStore.getState().setPreference('CREDIT')
  useSystemConfigStore.getState().setConfig(mapStatusDataToConfig(v2))
})
afterEach(() => {
  useSystemConfigStore.setState({ config: initial })
  useWalletCurrencyPreferenceStore.getState().setPreference(preference)
  useAuthStore.setState({ auth: initialAuth })
})

test('public credits change balances and model quotes without revaluing the ledger or fiat', () => {
  const config = currencyConfig()
  assert.equal(config.creditsPerUsd, 3359744)
  assert.equal(config.ledgerQuotaPerUsd, 3359744)
  assert.equal(config.publicCreditsPerUsd, 100000)
  assert.equal(quotaToDisplayAmount(3359744, 'USD', config), 1)
  assert.equal(quotaToDisplayAmount(3359744, 'CNY', config), 6.719488)
  assert.equal(quotaToDisplayAmount(3359744, 'CREDIT', config), 100000)
  assert.equal(
    3359744 / quotaToDisplayAmount(3359744, 'CREDIT', config),
    33.59744
  )
  assert.equal(formatCreditAmount(3359744, options, config), '100,000 Credits')
  assert.equal(
    formatQuotaInCurrency(3359744, 'CNY', options, config),
    '6.719488 CNY'
  )
  assert.equal(formatQuotaInCurrency(3359744, 'USD', options, config), '1 USD')
  assert.equal(formatModelPrice(4, 'USD', options), '4 USD')
  assert.equal(formatModelPrice(4, 'CREDIT', options), '400,000 Credits')
  assert.equal(
    formatUSDInCurrency(0.0000001, 'CREDIT', options, config),
    '0.01 Credits'
  )
  assert.equal(formatUSDInCurrency(0, 'CREDIT', options, config), '0 Credits')
  assert.equal(
    formatAmountInCurrency(100000, 'CREDIT', options),
    '100,000 Credits'
  )
})

test('display inputs and normal test-key callbacks submit unchanged internal ledger quota', () => {
  const config = currencyConfig()
  for (const [unit, amount] of [
    ['USD', '1'],
    ['CNY', '6.719488'],
    ['CREDIT', '100000'],
  ] as const) {
    assert.equal(displayAmountToQuota(amount, unit, config), 3359744)
    useWalletCurrencyPreferenceStore.getState().setPreference(unit)
    assert.equal(parseQuotaFromDollars(amount), 3359744)
    assert.equal(testKeyQuota(amount), 3359744)
    assert.equal(testKeyPayload(3359744, 'default', 0).remain_quota, 3359744)
    assert.equal(quotaUnitsToDollars(3359744), Number(amount))
  }
  assert.equal(displayAmountToQuota('1', 'CREDIT', config), 33)
  assert.equal(displayAmountToQuota('1.9', 'CREDIT', config), 63)
  assert.equal(legacyPlatformAmountToQuota('6.719488', config), 3359744)
  assert.equal(quotaToLegacyPlatformAmount(3359744, config), 6.719488)
  assert.equal(legacyPlatformAmountToQuota('1', config), 500000)
  useWalletCurrencyPreferenceStore.getState().setPreference('CREDIT')
  assert.equal(getEditableQuotaStep(), 'any')
})

test('one ledger unit and signed max-safe balances retain exact editable round trips in all three units', () => {
  const config = currencyConfig()
  assert.equal(formatCreditAmount(1, options, config), '0.029764 Credits')
  for (const unit of ['USD', 'CNY', 'CREDIT'] as const) {
    for (const quota of [
      0,
      1,
      -1,
      3359744,
      Number.MAX_SAFE_INTEGER,
      -Number.MAX_SAFE_INTEGER,
    ]) {
      const text = quotaToDisplayInput(quota, unit, config)
      assert.notEqual(text, '')
      assert.equal(displayAmountToQuota(text, unit, config), quota)
      if (quota >= 0) {
        assert.equal(
          marketQuota(text, (input) =>
            displayAmountToQuota(input, unit, config)
          ),
          quota
        )
      }
    }
    assert.ok(
      Number.isNaN(displayAmountToQuota('9007199254740992000000', unit, config))
    )
  }
})

test('old server status clears cached v2 fields and falls back only when every new field is absent', () => {
  useSystemConfigStore.getState().setConfig(
    mapStatusDataToConfig({
      currency_unit: 'credit',
      credits_per_usd: 3359744,
      cny_per_usd: '6.719488',
      quota_per_unit: 500000,
    })
  )
  assert.equal(getCurrencyDisplay().config.publicCreditsPerUsd, undefined)
  assert.equal(quotaToDisplayAmount(3359744, 'CREDIT'), 3359744)
  assert.equal(formatCreditAmount(1, options), '1 Credits')
  assert.equal(displayAmountToQuota('3359744', 'CREDIT'), 3359744)
  assert.equal(quotaToDisplayAmount(3359744, 'USD'), 1)
  assert.equal(getEditableQuotaStep(), 1)
})

test('invalid public metadata cannot fall back to the large legacy credit face value', () => {
  for (const bad of [
    { public_credits_per_usd: 0 },
    { public_credits_per_usd: -1 },
    { public_credits_per_usd: Infinity },
    { public_credits_per_usd: undefined },
    { public_credits_per_usd_exact: '99999' },
    { public_credits_per_usd_exact: '100000.000000000001' },
    { public_credits_per_usd_exact: '' },
    { public_credit_unit: undefined },
    { public_credit_unit: 'LEDGER_QUOTA' },
  ]) {
    const config = currencyConfig({ ...v2, ...bad })
    assert.equal(formatCreditAmount(3359744, options, config), '-')
    assert.equal(formatUSDInCurrency(4, 'CREDIT', options, config), '-')
    assert.ok(Number.isNaN(displayAmountToQuota('100000', 'CREDIT', config)))
    assert.equal(quotaToDisplayInput(1, 'CREDIT', config), '')
    assert.equal(quotaToDisplayAmount(3359744, 'USD', config), 1)
  }
  for (const bad of [
    { credit_unit_schema_version: undefined },
    { credit_unit_schema_version: 3 },
    { ledger_quota_per_usd: undefined },
    { ledger_quota_per_usd: 100000 },
    { ledger_quota_per_usd_exact: '3359745' },
    { ledger_quota_per_usd_exact: '3359744.00000000001' },
    { credits_per_usd: 100000 },
    { quota_unit: 'CREDIT' },
    { legacy_credit_unit: 'CREDIT' },
  ]) {
    const config = currencyConfig({ ...v2, ...bad })
    assert.equal(formatCreditAmount(3359744, options, config), '-')
    assert.ok(Number.isNaN(quotaToDisplayAmount(3359744, 'USD', config)))
    assert.ok(Number.isNaN(displayAmountToQuota('1', 'USD', config)))
    assert.equal(formatUSDInCurrency(4, 'USD', options, config), '4 USD')
  }
})

test('a different configured face value updates public credits while explicit USD remains independent of preference and FX', () => {
  const config = currencyConfig({
    ...v2,
    public_credits_per_usd: 250000,
    public_credits_per_usd_exact: '250000',
  })
  assert.equal(quotaToDisplayAmount(3359744, 'CREDIT', config), 250000)
  assert.equal(
    formatUSDInCurrency(4, 'CREDIT', options, config),
    '1,000,000 Credits'
  )
  assert.equal(displayAmountToQuota('250000', 'CREDIT', config), 3359744)
  assert.equal(formatQuotaInCurrency(3359744, 'USD', options, config), '1 USD')
  assert.equal(formatUSDInCurrency(4, 'USD', options, config), '4 USD')
  assert.equal(quotaToDisplayAmount(3359744, 'CNY', config), 6.719488)
})

test('actual chart sums, axes and tooltips retain fractional public credits without converting twice', () => {
  const rows = [
    {
      created_at: 1720000000,
      model_name: 'a',
      username: 'alice',
      quota: 3359744,
      count: 1,
    },
    {
      created_at: 1720000000,
      model_name: 'b',
      username: 'bob',
      quota: 1,
      count: 1,
    },
  ]
  const model = processChartData(rows, 'day')
  const user = processUserChartData(rows, 'day')
  const values = model.spec_line.data[0].values
  assert.equal(values[0].Usage, 100000)
  assert.equal(values[1].Usage, 100000 / 3359744)
  assert.equal(
    model.spec_line.axes[1].label.formatMethod(100000),
    '100,000 Credits'
  )
  assert.equal(
    model.spec_line.axes[1].label.formatMethod(values[1].Usage),
    '0.02976417 Credits'
  )
  assert.equal(
    user.spec_user_rank.label.formatMethod(values[1].Usage),
    '0.02976417 Credits'
  )
  assert.equal(model.totalQuotaDisplay, '100,000.03 Credits')
  const tooltip = model.spec_line.tooltip.dimension.updateContent(
    values.map((row: { Model: string; rawQuota: number }) => ({
      key: row.Model,
      value: row.rawQuota,
      datum: row,
    }))
  )
  assert.equal(tooltip[0].value, '100,000.0298 Credits')
})

test('very small public rates remain positive rather than rendering as free', () => {
  const config = currencyConfig({
    ...v2,
    public_credits_per_usd: 1e-20,
    public_credits_per_usd_exact: '1e-20',
  })
  assert.equal(formatCreditAmount(1, options, config), '2.98e-27 Credits')
  const text = quotaToDisplayInput(1, 'CREDIT', config)
  assert.equal(displayAmountToQuota(text, 'CREDIT', config), 1)
  assert.equal(
    formatUSDInCurrency(1e-10, 'CREDIT', options, config),
    '1.00e-30 Credits'
  )
  assert.equal(formatUSDInCurrency(1e-310, 'CREDIT', options, config), '-')
})

test('ledger anchors other than the historic deployment value retain equivalent public and fiat amounts', () => {
  for (const ledgerK of [500000, 3000000]) {
    const config = currencyConfig({
      ...v2,
      credits_per_usd: ledgerK,
      ledger_quota_per_usd: ledgerK,
      ledger_quota_per_usd_exact: String(ledgerK),
    })
    assert.equal(quotaToDisplayAmount(ledgerK, 'CREDIT', config), 100000)
    assert.equal(quotaToDisplayAmount(ledgerK, 'USD', config), 1)
    assert.equal(displayAmountToQuota('100000', 'CREDIT', config), ledgerK)
  }
  const identity = currencyConfig({
    ...v2,
    public_credits_per_usd: 3359744,
    public_credits_per_usd_exact: '3359744',
  })
  assert.equal(displayAmountToQuota('1.9', 'CREDIT', identity), 1)
})
