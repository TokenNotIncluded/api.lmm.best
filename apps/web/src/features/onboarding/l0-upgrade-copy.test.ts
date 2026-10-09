/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'

const keys = [
  'Upgrade to a full account',
  'Chat for free or top up to upgrade to a full account.',
  'Chat for free to upgrade to a full account.',
  'Chat (free)',
  'Top up to upgrade',
  'Paid upgrade details',
]

for (const locale of ['en', 'zh', 'zh-TW', 'fr', 'ru', 'ja', 'vi']) {
  test(`${locale} provides both upgrade paths without an English fallback`, () => {
    const { translation } = JSON.parse(
      readFileSync(
        new URL(`../../i18n/locales/${locale}.json`, import.meta.url),
        'utf8'
      )
    ) as { translation: Record<string, string> }
    for (const key of keys) {
      assert.ok(translation[key]?.trim(), key)
      if (locale !== 'en') assert.notEqual(translation[key], key)
    }
    if (locale === 'zh') {
      assert.equal(
        translation[keys[1]],
        '通过对话（免费）或充值，升级为正式用户。'
      )
      assert.equal(translation[keys[3]], '对话（免费）')
      assert.equal(translation[keys[4]], '充值升级')
    }
  })
}
