/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createInstance } from 'i18next'
import { providerNamespace, providerTranslations, registerProviderTranslations } from './provider-i18n'

const placeholders = (s: string) => [...s.matchAll(/{{([^}]+)}}/g)].map(m => m[1]).sort()
test('provider copy has all seven languages and preserves amount placeholders', () => {
  const english = providerTranslations.en
  for (const [language, copy] of Object.entries(providerTranslations)) {
    assert.deepEqual(Object.keys(copy), Object.keys(english))
    for (const [key, value] of Object.entries(copy)) {
      assert.ok(value.trim())
      assert.deepEqual(placeholders(value), placeholders(english[key]))
      if (language !== 'en') assert.notEqual(value, english[key])
    }
  }
})
test('provider copy registers separately, keeps custom copy and falls back to global labels', async () => {
  const instance = createInstance()
  await instance.init({lng: 'zhCN', fallbackLng: 'en', resources: {zhCN: {translation: {Save: '保存'}}}})
  registerProviderTranslations(instance)
  instance.addResource('zhCN', providerNamespace, 'Provider preset', '自定义平台标题')
  registerProviderTranslations(instance)
  const t = instance.getFixedT(null, [providerNamespace, 'translation'])
  assert.equal(t('Provider preset'), '自定义平台标题')
  assert.equal(t('Browser login (recommended)'), '浏览器登录（推荐）')
  assert.equal(t('Save'), '保存')
  assert.equal(t('Upstream price × {{multiplier}}; maximum {{amount}} per call', {multiplier: '1.2', amount: '$1'}), '上游价格 × 1.2；每次最多 $1')
  assert.equal(instance.hasResourceBundle('fr', 'translation'), false)
})
