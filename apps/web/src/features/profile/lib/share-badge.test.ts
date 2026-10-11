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
  BADGE_PALETTES,
  INITIAL_OPTIONS,
  badgeLanguage,
  buildBadgeURL,
  buildBadgeEmbedCode,
  changeBadgeLayout,
  canShareBadge,
} from './share-badge'

test('brand presets use the monochrome ribbon identity and readable contrast', () => {
  const luminance = (color: string) => {
    const channels = [1, 3, 5].map((start) => {
      const value = parseInt(color.slice(start, start + 2), 16) / 255
      return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
    })
    return channels[0] * 0.2126 + channels[1] * 0.7152 + channels[2] * 0.0722
  }
  assert.deepEqual(BADGE_STYLE_PRESETS.map((preset) => preset.name), [
    'Graphite',
    'Paper',
    'Clear',
  ])
  assert.equal(BADGE_PALETTES.paper.bg, '#ffffff')
  assert.equal(BADGE_PALETTES.dark.fg, '#ffffff')
  for (const preset of BADGE_STYLE_PRESETS) {
    assert.equal(preset.font, 'sans')
    assert.equal(preset.radius, 16)
    assert.ok(
      !['#d97757', '#3fb950', '#6fb7ff', '#a78bfa'].includes(preset.colors.accent)
    )
    for (const key of ['fg', 'muted'] as const) {
      const foreground = luminance(preset.colors[key])
      const background = luminance(preset.colors.bg)
      const contrast =
        (Math.max(foreground, background) + 0.05) /
        (Math.min(foreground, background) + 0.05)
      assert.ok(contrast >= 4.5, `${preset.name} ${key} contrast: ${contrast}`)
    }
  }
})

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

test('aggregate sharing uses independent consent and excludes model-only SVG parameters', () => {
  assert.equal(
    canShareBadge({ enabled: true, model_usage_enabled: true }, 'aggregate'),
    false
  )
  assert.equal(
    canShareBadge(
      { enabled: true, aggregate_usage_enabled: true },
      'aggregate'
    ),
    true
  )
  assert.equal(
    canShareBadge(
      { enabled: false, aggregate_usage_enabled: true },
      'aggregate'
    ),
    false
  )
  const options = changeBadgeLayout(INITIAL_OPTIONS, 'aggregate')
  const url = new URL(
    buildBadgeURL('https://api.lmm.best/example.svg', options, 'en')
  )
  assert.equal(url.searchParams.get('layout'), 'aggregate')
  assert.equal(url.searchParams.get('animation'), 'none')
  assert.equal(url.searchParams.has('top'), false)
  assert.equal(options.height, 720)
  assert.match(
    buildBadgeEmbedCode(url.toString(), options).markdown,
    /AI usage across linked profiles/
  )
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

test('model SVG exports preserve the selected money currency independently of language', () => {
  for (const currency of ['CREDIT', 'CNY', 'USD'] as const) {
    const url = new URL(
      buildBadgeURL(
        'https://api.lmm.best/example.svg',
        INITIAL_OPTIONS,
        'zhCN',
        currency
      )
    )
    assert.equal(url.searchParams.get('currency'), currency)
    assert.equal(url.searchParams.get('lang'), 'zh')
    for (const layout of ['profile', 'badge', 'aggregate'] as const) {
      const nonMoneyURL = new URL(
        buildBadgeURL(
          'https://api.lmm.best/example.svg',
          changeBadgeLayout(INITIAL_OPTIONS, layout),
          'en',
          currency
        )
      )
      assert.equal(nonMoneyURL.searchParams.has('currency'), false)
    }
  }
  const automatic = new URL(
    buildBadgeURL('https://api.lmm.best/example.svg', INITIAL_OPTIONS, 'en')
  )
  assert.equal(automatic.searchParams.has('currency'), false)
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
    `[![LMM API model usage](${url})](https://api.lmm.best)`
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
