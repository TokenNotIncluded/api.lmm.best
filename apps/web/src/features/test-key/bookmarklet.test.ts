/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { afterEach, describe, test } from 'node:test'
import vm from 'node:vm'

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

afterEach(() =>
  useSystemConfigStore
    .getState()
    .setConfig({ currency: DEFAULT_CURRENCY_CONFIG })
)

describe('test key bookmark', () => {
  test('opens only a first-party popup with no opener, payload, or credential', () => {
    const calls: unknown[][] = []
    const script = buildTestKeyBookmarklet(
      'https://api.lmm.best/ignored?secret=ignored'
    )
    assert.equal(script.startsWith('javascript:'), true)
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
    useSystemConfigStore.getState().setConfig({
      currency: {
        ...DEFAULT_CURRENCY_CONFIG,
        quotaDisplayType: 'CNY',
        usdExchangeRate: 7,
      },
    })
    assert.equal(testKeyQuota('7'), 500000)
    useSystemConfigStore.getState().setConfig({
      currency: { ...DEFAULT_CURRENCY_CONFIG, quotaDisplayType: 'TOKENS' },
    })
    assert.equal(testKeyQuota('5'), 5)
    assert.throws(() => testKeyPayload(0, 'auto', 0))
    assert.throws(() => testKeyPayload(500, '', 0))
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
