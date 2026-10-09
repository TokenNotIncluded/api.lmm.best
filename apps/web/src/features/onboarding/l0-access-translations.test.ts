/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/

import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { test } from 'node:test'

const staleReview = {
  zh: '此账户需通过审核开通。',
  'zh-TW': '此帳戶須通過審核開通。',
  fr: 'Ce compte nécessite une validation.',
  ja: 'このアカウントの有効化には審査が必要です。',
  vi: 'Tài khoản này cần được xét duyệt.',
  ru: 'Для этого аккаунта требуется проверка.',
}
for (const lang of ['en', ...Object.keys(staleReview)]) {
  test(`${lang} explains activation without restoring an application requirement`, async () => {
    const { translation } = JSON.parse(
      await readFile(
        new URL(`../../i18n/locales/${lang}.json`, import.meta.url),
        'utf8'
      )
    )
    const paid = translation['Top up {{amount}} to enable L1 immediately.']
    assert.ok(paid.includes('{{amount}}'))
    assert.ok(paid.includes('L1'))
    assert.ok(
      translation[
        'Describe what you need. The assistant can enable L1 without an application letter.'
      ]
    )
    if (Object.hasOwn(staleReview, lang)) {
      assert.notEqual(
        translation[
          'Automatic paid activation is unavailable for this account.'
        ],
        staleReview[lang as keyof typeof staleReview]
      )
    }
    assert.equal(
      translation['Top up {{amount}} for instant approval.'],
      undefined
    )
  })
}
