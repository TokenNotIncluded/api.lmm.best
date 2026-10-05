/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { mapStatusDataToConfig } from './use-system-config'

test('hydrates authoritative denomination and decimal text without relying on a recharge price', () => {
  const config = mapStatusDataToConfig({
    currency_unit: 'credit',
    credits_per_usd: '3365431.5',
    cny_per_usd: '6.730863',
    legacy_pricing_units_per_usd: '6.730863',
    quota_per_unit: 500000,
    usd_exchange_rate: 99,
  }).currency
  assert.equal(config?.currencyUnit, 'credit')
  assert.equal(config?.creditsPerUsd, 3365431.5)
  assert.equal(config?.creditsPerUsdExact, '3365431.5')
  assert.equal(config?.cnyPerUsd, 6.730863)
  assert.equal(config?.cnyPerUsdExact, '6.730863')
  assert.equal(config?.legacyPricingUnitsPerUsd, 6.730863)
})

test('missing denomination remains unknown rather than inventing a 1:1 fiat mapping', () => {
  const currency = mapStatusDataToConfig({ usd_exchange_rate: 7.3 }).currency
  assert.equal(currency?.currencyUnit, 'unknown')
  assert.equal(currency?.creditsPerUsd, 0)
  assert.equal(currency?.cnyPerUsd, 0)
})

test('rejects invalid rate values instead of reusing a default or cached exchange rate', () => {
  for (const invalid of [0, -1, '', 'Infinity', Number.POSITIVE_INFINITY]) {
    const currency = mapStatusDataToConfig({
      currency_unit: 'credit',
      credits_per_usd: invalid,
      cny_per_usd: invalid,
    }).currency
    assert.equal(currency?.creditsPerUsd, 0)
    assert.equal(currency?.cnyPerUsd, 0)
    assert.equal(currency?.creditsPerUsdExact, '')
    assert.equal(currency?.cnyPerUsdExact, '')
  }
})
