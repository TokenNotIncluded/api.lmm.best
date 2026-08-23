/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import path from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'

const locales = ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi'] as const
const testDirectory = path.dirname(fileURLToPath(import.meta.url))
const subscriptionDeleteError =
  'Subscription plan has subscription or order history and cannot be deleted. Disable it instead.'

test('locale files keep every UI key inside the translation namespace', async () => {
  for (const locale of locales) {
    const file = path.join(testDirectory, 'locales', `${locale}.json`)
    const json = JSON.parse(await fs.readFile(file, 'utf8')) as Record<
      string,
      unknown
    >
    assert.deepEqual(Object.keys(json), ['translation'], locale)

    const translation = json.translation as Record<string, string>
    assert.equal(typeof translation[subscriptionDeleteError], 'string', locale)
    assert.ok(translation[subscriptionDeleteError].length > 0, locale)
    if (locale !== 'en') {
      assert.notEqual(
        translation[subscriptionDeleteError],
        subscriptionDeleteError,
        `${locale} must translate the subscription deletion guard`
      )
    }
  }
})
