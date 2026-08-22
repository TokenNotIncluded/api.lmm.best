/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { discountCodeTranslations } from '@/features/discount-codes/i18n.js'
import { heroSmsTranslations } from '@/features/email-activations/i18n.js'

const locales = ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi'] as const

function placeholders(value: string) {
  return [...value.matchAll(/{{\s*([^},\s]+)[^}]*}}/g)]
    .map((match) => match[1])
    .sort()
}

function assertCompleteFeatureTranslations(
  resources: Record<(typeof locales)[number], Record<string, string>>
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
    assertCompleteFeatureTranslations(heroSmsTranslations)
  })

  test('discount cleanup resources are complete in every supported locale', () => {
    assertCompleteFeatureTranslations(discountCodeTranslations)
  })
})
