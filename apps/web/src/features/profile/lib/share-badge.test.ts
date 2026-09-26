/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  BADGE_STYLE_PRESETS,
  INITIAL_OPTIONS,
  badgeLanguage,
  buildBadgeURL,
  buildBadgeEmbedCode,
  changeBadgeLayout,
  canShareBadge,
} from './share-badge'

test('model sharing is separate from an existing profile URL', () => {
  assert.equal(canShareBadge({ enabled: true }, 'models'), false)
  assert.equal(canShareBadge({ enabled: true }, 'profile'), true)
  assert.equal(
    canShareBadge({ enabled: true, model_usage_enabled: true }, 'models'),
    true
  )
  assert.equal(
    canShareBadge({ enabled: false, model_usage_enabled: true }, 'models'),
    false
  )
  assert.equal(canShareBadge(undefined, 'models'), false)
})

test('all appearance presets round-trip into model SVG URLs', () => {
  for (const preset of BADGE_STYLE_PRESETS) {
    const options = {
      ...INITIAL_OPTIONS,
      ...preset,
      top: 9,
      period: '7d' as const,
      title: 'A & B <title>',
      requests: false,
    }
    const url = new URL(
      buildBadgeURL(
        'https://api.lmm.best/api/share/profile/example.svg?obsolete=1#old',
        options,
        'zhCN'
      )
    )
    assert.equal(url.searchParams.get('layout'), 'models')
    assert.equal(url.searchParams.get('period'), '7d')
    assert.equal(url.searchParams.get('top'), '9')
    assert.equal(url.searchParams.get('theme'), preset.theme)
    assert.equal(url.searchParams.get('accent'), preset.colors.accent)
    assert.equal(url.searchParams.get('font'), preset.font)
    assert.equal(url.searchParams.get('animation'), preset.animation)
    assert.equal(url.searchParams.get('title'), options.title)
    assert.equal(url.searchParams.get('requests'), '0')
    assert.equal(url.searchParams.get('lang'), 'zh')
    assert.equal(url.searchParams.has('bg'), preset.theme !== 'transparent')
    assert.equal(url.searchParams.has('obsolete'), false)
    assert.equal(url.hash, '')
  }
})

test('legacy layouts never receive model-only parameters or invalid periods', () => {
  const profile = changeBadgeLayout(INITIAL_OPTIONS, 'profile')
  assert.equal(profile.period, '365d')
  assert.equal(profile.height, 865)
  const badge = changeBadgeLayout(INITIAL_OPTIONS, 'badge')
  assert.equal(badge.height, 240)
  for (const options of [profile, badge]) {
    const url = new URL(
      buildBadgeURL('https://api.lmm.best/example.svg', options, 'en')
    )
    assert.equal(url.searchParams.has('top'), false)
  }
  const model = changeBadgeLayout({ ...badge, period: 'all' }, 'models')
  assert.equal(model.period, '30d')
  assert.equal(model.height, 900)
})

test('SVG language maps interface and browser locale aliases', () => {
  for (const [input, expected] of [
    ['zhCN', 'zh'],
    ['zh-CN', 'zh'],
    ['zhTW', 'zh-TW'],
    ['zh-Hant', 'zh-TW'],
    ['ja-JP', 'ja'],
    ['fr-FR', 'fr'],
    ['unsupported', 'en'],
  ]) {
    assert.equal(badgeLanguage(input), expected)
  }
})

test('copy embeds only the chosen live image, never a stale statistics snapshot', () => {
  const url = buildBadgeURL(
    'https://api.lmm.best/example.svg',
    INITIAL_OPTIONS,
    'en'
  )
  const code = buildBadgeEmbedCode(url, INITIAL_OPTIONS)
  assert.equal(
    code.markdown,
    `[![LMM Best model usage](${url})](https://api.lmm.best)`
  )
  assert.ok(code.html.includes('&amp;'))
  assert.ok(code.html.includes('width="1200" height="900"'))
  assert.ok(!code.markdown.includes('###'))
  const unsafe = buildBadgeEmbedCode(
    'https://example.test/?x="<>&',
    INITIAL_OPTIONS
  )
  assert.ok(unsafe.html.includes('&quot;&lt;&gt;&amp;'))
  assert.deepEqual(buildBadgeEmbedCode('', INITIAL_OPTIONS), {
    markdown: '',
    html: '',
  })
})
