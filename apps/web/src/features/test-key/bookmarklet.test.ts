/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { afterEach, beforeEach, describe, test } from 'node:test'
import vm from 'node:vm'

import { useAuthStore } from '@/stores/auth-store'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import {
  buildTestKeyBookmarklet,
  TEST_KEY_WINDOW_FEATURES,
  testKeyUrl,
} from './bookmarklet'
import { testKeyTranslations } from './copy'
import { testKeyPayload, testKeyQuota } from './test-key'

const originalConfig = useSystemConfigStore.getState().config
const originalAuth = useAuthStore.getState().auth
const fixedCurrencyConfig = {
  ...DEFAULT_CURRENCY_CONFIG,
  currencyUnit: 'credit' as const,
  creditsPerUsd: 500000,
  creditsPerUsdExact: '500000',
  cnyPerUsd: 6.8,
  cnyPerUsdExact: '6.8',
  legacyPricingUnitsPerUsd: 1,
  quotaPerUnit: 500000,
  // These legacy display settings must not define the new denomination.
  quotaDisplayType: 'CNY' as const,
  usdExchangeRate: 99,
}
function setDisplayCurrency(value: 'CREDIT' | 'CNY' | 'USD') {
  useAuthStore.getState().auth.setUser({
    id: 77,
    username: 'fixture',
    role: 1,
    setting: { wallet_display_currency: value },
  })
}
function setCurrencyConfig(currency: typeof fixedCurrencyConfig) {
  useSystemConfigStore.setState({ config: { ...originalConfig, currency } })
}
beforeEach(() => {
  setDisplayCurrency('USD')
  setCurrencyConfig(fixedCurrencyConfig)
})
afterEach(() => {
  useSystemConfigStore.setState({ config: originalConfig })
  useAuthStore.setState({ auth: originalAuth })
})

describe('test key bookmark', () => {
  test('opens only a first-party popup with no opener, payload, or credential', () => {
    const calls: unknown[][] = []
    const script = buildTestKeyBookmarklet(
      'https://api.lmm.best/ignored?secret=ignored'
    )
    assert.equal(new URL(script).protocol, 'javascript:')
    assert.equal(script.includes('secret'), false)
    assert.equal(
      vm.runInNewContext(script.slice('javascript:'.length), {
        window: {
          open: (...args: unknown[]) => {
            calls.push(args)
            return null
          },
        },
      }),
      undefined
    )
    assert.deepEqual(calls, [
      ['https://api.lmm.best/test-key', '_blank', TEST_KEY_WINDOW_FEATURES],
    ])
    assert.match(TEST_KEY_WINDOW_FEATURES, /noopener,noreferrer/)
  })
  test('rejects script URLs, credentials and non-web origins', () => {
    for (const origin of [
      'javascript:alert(1)',
      'data:text/html,hello',
      'https://user:pass@api.lmm.best',
      'file:///test',
    ]) {
      assert.throws(() => testKeyUrl(origin))
    }
    assert.equal(
      testKeyUrl('http://localhost:3000'),
      'http://localhost:3000/test-key'
    )
  })
  test('creates a quota-only key using the existing reveal policy', () => {
    const payload = testKeyPayload(500000, 'auto', 0)
    assert.equal(payload.one_time_reveal, true)
    assert.equal(payload.expired_time, -1)
    assert.equal(payload.unlimited_quota, false)
    assert.equal(payload.remain_quota, 500000)
    assert.equal(payload.model_limits_enabled, false)
    assert.equal(payload.model_limits, '')
    assert.equal(payload.allow_ips, '')
    assert.equal('max_requests' in payload, false)
    assert.equal(payload.group, 'auto')
    assert.deepEqual(payload.auto_groups, [])
    assert.match(payload.name, /^test-\d{4}-\d{2}-\d{2}-[a-f0-9]{8}$/)
    assert.ok(payload.name.length <= 50)
  })
  test('validates quota and respects configured currency conversion', () => {
    for (const value of [
      '',
      ' ',
      '0',
      '-1',
      'NaN',
      'Infinity',
      '1e99',
      '1e-100',
    ]) {
      assert.equal(testKeyQuota(value), null)
    }
    assert.equal(testKeyQuota('1'), 500000)
    setDisplayCurrency('CNY')
    assert.equal(testKeyQuota('6.8'), 500000)
    assert.equal(testKeyQuota('1'), 73529)
    setDisplayCurrency('CREDIT')
    assert.equal(testKeyQuota('5'), 5)
    assert.equal(testKeyQuota('1.5'), null)
    assert.equal(testKeyQuota('1.000000000000000000000000000001'), null)
    assert.throws(() => testKeyPayload(0, 'auto', 0))
    assert.throws(() => testKeyPayload(500, '', 0))
  })
  test('preserves one Credit through literal USD and CNY decimal inputs', () => {
    assert.equal(testKeyQuota('0.000002'), 1)
    assert.equal(testKeyQuota('0.000001999999999999999999999999'), null)
    setDisplayCurrency('CNY')
    assert.equal(testKeyQuota('0.0000136'), 1)
    assert.equal(testKeyQuota('0.000013599999999999999999999999'), null)
    setDisplayCurrency('CREDIT')
    assert.equal(testKeyQuota('1'), 1)
    assert.equal(testKeyQuota('9007199254740991'), 9007199254740991)
    assert.equal(testKeyQuota('9007199254740992'), null)
  })
  test('missing or invalid fixed denomination never guesses a fiat conversion', () => {
    for (const creditsPerUsd of [0, -1, Number.NaN, Infinity]) {
      setCurrencyConfig({
        ...fixedCurrencyConfig,
        creditsPerUsd,
        creditsPerUsdExact: '',
      })
      assert.equal(testKeyQuota('1'), null)
    }
    setDisplayCurrency('CNY')
    setCurrencyConfig({
      ...fixedCurrencyConfig,
      cnyPerUsd: 0,
      cnyPerUsdExact: '',
    })
    assert.equal(testKeyQuota('1'), null)
    setDisplayCurrency('CREDIT')
    assert.equal(testKeyQuota('1'), 1)
  })
  test('all supported locales have the same keys and interpolation tokens', () => {
    const reference = testKeyTranslations.en
    for (const translation of Object.values(testKeyTranslations)) {
      assert.deepEqual(Object.keys(translation), Object.keys(reference))
      for (const key of Object.keys(reference) as Array<
        keyof typeof reference
      >) {
        assert.ok(translation[key].trim())
        assert.deepEqual(
          translation[key].match(/{{\w+}}/g),
          reference[key].match(/{{\w+}}/g)
        )
      }
    }
  })
  test('the tutorial has a reduced-motion alternative and pause support', () => {
    const css = readFileSync(
      new URL('./bookmarklet.css', import.meta.url),
      'utf8'
    )
    assert.match(css, /prefers-reduced-motion: reduce/)
    assert.match(css, /animation: none/)
    assert.match(css, /animation-play-state: paused/)
  })
})
