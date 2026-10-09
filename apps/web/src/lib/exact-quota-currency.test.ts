/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { mapStatusDataToConfig } from '@/hooks/use-system-config'
import { INTERFACE_LANGUAGE_OPTIONS, toIntlLocale } from '@/i18n/languages'
import { DEFAULT_CURRENCY_CONFIG } from '@/stores/system-config-store'

import { formatExactQuotaInCurrency } from './currency'

const config = {
  ...DEFAULT_CURRENCY_CONFIG,
  currencyUnit: 'credit' as const,
  creditsPerUsd: 500000,
  creditsPerUsdExact: '500000',
  cnyPerUsd: 7,
  cnyPerUsdExact: '7',
}
const options = { locale: 'en', showSymbol: false }

test('large signed aggregate conversions preserve exact integer and cent rounding', () => {
  assert.equal(
    formatExactQuotaInCurrency(
      '9277415232383220730',
      'CREDIT',
      options,
      config
    ),
    '9,277,415,232,383,220,730'
  )
  assert.equal(
    formatExactQuotaInCurrency('9277415232383220730', 'USD', options, config),
    '18,554,830,464,766.44'
  )
  assert.equal(
    formatExactQuotaInCurrency('9277415232383220730', 'CNY', options, config),
    '129,883,813,253,365.09'
  )
  assert.equal(
    formatExactQuotaInCurrency('-9007199254740993', 'CREDIT', options, config),
    '-9,007,199,254,740,993'
  )
  assert.equal(
    formatExactQuotaInCurrency('-9007199254740993', 'CNY', options, config),
    '-126,100,789,566.37'
  )
  for (const value of ['502500', '-502500']) {
    assert.equal(
      formatExactQuotaInCurrency(value, 'USD', options, config),
      value.startsWith('-') ? '-1.01' : '1.01'
    )
  }
})

test('small balances keep their sign and do not round to a fake zero', () => {
  assert.equal(
    formatExactQuotaInCurrency('1', 'USD', options, config),
    '0.000002'
  )
  assert.equal(
    formatExactQuotaInCurrency('-1', 'USD', options, config),
    '-0.000002'
  )
  assert.equal(
    formatExactQuotaInCurrency('1', 'CNY', options, {
      ...config,
      cnyPerUsd: 0.001,
      cnyPerUsdExact: '0.001',
    }),
    '0.000000002'
  )
  assert.equal(formatExactQuotaInCurrency('0', 'USD', options, config), '0')
})

for (const { code } of INTERFACE_LANGUAGE_OPTIONS) {
  test(`exact aggregate display supports ${code} separators and labels`, () => {
    const locale = toIntlLocale(code)
    assert.equal(
      formatExactQuotaInCurrency('617283500', 'USD', { locale }, config),
      `${new Intl.NumberFormat(locale).format(1234.57)} USD`
    )
    assert.equal(
      formatExactQuotaInCurrency(
        '9277415232383220730',
        'CREDIT',
        { locale, creditLabel: 'points' },
        config
      ),
      `${new Intl.NumberFormat(locale).format(9277415232383220730n)} points`
    )
  })
}

test('invalid integers and unavailable conversion metadata stay unknown', () => {
  for (const value of ['', '1.5', 'NaN', '1e3', ' 1', '9'.repeat(513)]) {
    assert.equal(formatExactQuotaInCurrency(value, 'USD', options, config), '-')
  }
  assert.equal(
    formatExactQuotaInCurrency('500000', 'CNY', options, {
      ...config,
      cnyPerUsd: 0,
    }),
    '-'
  )
  assert.equal(
    formatExactQuotaInCurrency(
      '500000',
      'USD',
      options,
      DEFAULT_CURRENCY_CONFIG
    ),
    '-'
  )
  const invalid = mapStatusDataToConfig({
    currency_unit: 'credit',
    credits_per_usd: 500000,
    ledger_quota_per_usd: 500000,
    ledger_quota_per_usd_exact: '500000',
    public_credits_per_usd: 100000,
    public_credits_per_usd_exact: '100000',
    credit_unit_schema_version: 2,
    quota_unit: 'LEDGER_QUOTA',
    public_credit_unit: 'CREDIT',
    cny_per_usd: 7,
  }).currency!
  assert.equal(
    formatExactQuotaInCurrency('500000', 'CREDIT', options, invalid),
    '-'
  )
  assert.equal(
    formatExactQuotaInCurrency('500000', 'USD', options, invalid),
    '1'
  )
})
