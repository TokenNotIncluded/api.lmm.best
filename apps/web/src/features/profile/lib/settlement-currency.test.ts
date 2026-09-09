/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  parseSettlementSettings,
  resolveSettlementCurrency,
  settlementCurrencyPreference,
} from './settlement-currency'

describe('fiat settlement preferences', () => {
  test('Chinese interface variants default to CNY; English and others default to USD', () => {
    for (const language of [
      'zh',
      'zh-CN',
      'zh-TW',
      'zh_Hant',
      ' ZH-CN,zh;q=0.9 ',
    ]) {
      assert.equal(resolveSettlementCurrency({}, language), 'CNY')
    }
    for (const language of ['en', 'en-US', 'fr', '', undefined]) {
      assert.equal(resolveSettlementCurrency({}, language), 'USD')
    }
  })

  test('saved currency overrides language and stored language precedes the hint', () => {
    assert.equal(
      resolveSettlementCurrency(
        { settlement_currency: 'USD', language: 'zh' },
        'zh-TW'
      ),
      'USD'
    )
    assert.equal(
      resolveSettlementCurrency(
        { settlement_currency: ' cny ', language: 'en' },
        'en'
      ),
      'CNY'
    )
    assert.equal(resolveSettlementCurrency({ language: 'en' }, 'zh'), 'USD')
    assert.equal(resolveSettlementCurrency({ language: 'zh-TW' }, 'en'), 'CNY')
    assert.equal(
      resolveSettlementCurrency({ settlement_currency: '', language: 'zh' }),
      'CNY'
    )
  })

  test('legacy malformed settings cannot invent a fiat conversion or erase parsed fields', () => {
    for (const value of ['broken-json', 'null', '[]', null, [], 42]) {
      assert.deepEqual(parseSettlementSettings(value), {})
    }
    assert.deepEqual(
      parseSettlementSettings(
        '{"settlement_currency":"CNY","record_ip_log":true}'
      ),
      {
        settlement_currency: 'CNY',
        record_ip_log: true,
      }
    )
    for (const value of ['EUR', 1, null, 'auto']) {
      assert.equal(settlementCurrencyPreference(value), '')
    }
    assert.equal(settlementCurrencyPreference(' usd '), 'USD')
  })
})
