/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  commerceImportAuthorizationUrl,
  commerceImportCount,
  commerceImportPriceQuota,
  commerceImportText,
} from './commerce-import-utils'

test('USD text uses the fixed exact denomination and rejects fractional ledger units', () => {
  assert.equal(commerceImportPriceQuota('1.25', 'USD'), 625000)
  assert.equal(commerceImportPriceQuota('0.000002', 'USD'), 1)
  assert.equal(commerceImportPriceQuota('001.000000', 'USD'), 500000)
  assert.equal(commerceImportPriceQuota('0', 'USD'), 0)
  for (const text of [
    '',
    '0.0000021',
    '1.0000001',
    '1e3',
    '-1',
    'NaN',
    '18014398510',
  ]) {
    assert.equal(commerceImportPriceQuota(text, 'USD'), undefined, text)
  }
})
test('raw price_quota accepts only safe nonnegative integer text', () => {
  assert.equal(commerceImportPriceQuota('500001', 'QUOTA'), 500001)
  assert.equal(commerceImportPriceQuota('0', 'QUOTA'), 0)
  for (const text of ['', '1.0', '1.2', '-1', '9007199254740992']) {
    assert.equal(commerceImportPriceQuota(text, 'QUOTA'), undefined, text)
  }
})
test('new card count rejects coercible decimals, signs and out-of-range integers', () => {
  assert.equal(commerceImportCount('1', 100), 1)
  assert.equal(commerceImportCount('50', 50), 50)
  for (const text of ['', '0', '51', '1.0', '2e1', '-1', ' 1 ']) {
    assert.equal(commerceImportCount(text, 50), undefined, text)
  }
})
test('authorization remains on the selected HTTPS issuer without credentials or fragments', () => {
  const issuer = 'https://redemption.example'
  assert.equal(
    commerceImportAuthorizationUrl(
      `${issuer}/oauth/authorize?state=public`,
      issuer
    ),
    `${issuer}/oauth/authorize?state=public`
  )
  for (const url of [
    'http://redemption.example/oauth/authorize',
    'https://other.example/oauth/authorize',
    'https://user@redemption.example/oauth/authorize',
    'https://redemption.example/oauth/authorize#token',
  ]) {
    assert.equal(commerceImportAuthorizationUrl(url, issuer), undefined)
  }
})
test('localized preview uses exactly the server mapping priority and sorted fallback', () => {
  assert.equal(commerceImportText({ en: 'English', 'zh-CN': '中文' }), '中文')
  assert.equal(commerceImportText({ ja: '日本語', de: 'Deutsch' }), 'Deutsch')
  assert.equal(commerceImportText({ 'zh-CN': ' ', en: 'English' }), 'English')
  assert.equal(commerceImportText('Plain text'), 'Plain text')
})
