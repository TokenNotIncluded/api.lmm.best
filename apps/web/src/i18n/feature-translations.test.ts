/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { createInstance } from 'i18next'

import { discountCodeTranslations } from '@/features/discount-codes/i18n.js'
import {
  heroSmsTranslations,
  registerHeroSmsTranslations,
} from '@/features/email-activations/i18n.js'
import {
  registerTemporaryActivationTranslations,
  temporaryActivationTranslations,
} from '@/features/email-activations/temporary-i18n.js'

const discountLocales = ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi'] as const
const heroSmsLocales = ['en', 'zhCN', 'zhTW', 'fr', 'ja', 'ru', 'vi'] as const

function placeholders(value: string) {
  return [...value.matchAll(/{{\s*([^},\s]+)[^}]*}}/g)]
    .map((match) => match[1])
    .sort()
}

function assertCompleteFeatureTranslations(
  resources: Record<string, Record<string, string>>,
  locales: readonly string[]
) {
  const englishKeys = Object.keys(resources.en).sort()
  assert.ok(englishKeys.length > 0)
  for (const locale of locales) {
    assert.deepEqual(Object.keys(resources[locale]).sort(), englishKeys)
    for (const key of englishKeys) {
      const value = resources[locale][key]
      assert.ok(value?.trim(), `${locale}: missing ${key}`)
      assert.deepEqual(placeholders(value), placeholders(resources.en[key]))
      if (locale !== 'en' && !/^(HeroSMS|USD|API Key)$/.test(key)) {
        assert.notEqual(
          value,
          resources.en[key],
          `${locale}: untranslated ${key}`
        )
      }
    }
  }
}

describe('lazy feature translations', () => {
  test('HeroSMS resources are complete in every supported locale', () => {
    assertCompleteFeatureTranslations(heroSmsTranslations, heroSmsLocales)
    assertCompleteFeatureTranslations(
      temporaryActivationTranslations,
      heroSmsLocales
    )
    assert.equal(heroSmsTranslations.zhCN['Purchase activation'], '购买接码')
    assert.equal(heroSmsTranslations.zhTW['Purchase activation'], '購買接碼')
    assert.equal(
      temporaryActivationTranslations.zhCN['Temporary activations'],
      '临时接码'
    )
  })

  test('registers HeroSMS resources under runtime locale codes', async () => {
    const instance = createInstance()
    await instance.init({ lng: 'zhCN', fallbackLng: 'en' })
    registerHeroSmsTranslations(instance)
    registerTemporaryActivationTranslations(instance)

    assert.equal(instance.t('Purchase activation'), '购买接码')
    assert.equal(instance.t('Temporary activations'), '临时接码')
    await instance.changeLanguage('zhTW')
    assert.equal(instance.t('Purchase activation'), '購買接碼')
  })

  test('discount cleanup resources are complete in every supported locale', () => {
    assertCompleteFeatureTranslations(discountCodeTranslations, discountLocales)
  })
})
