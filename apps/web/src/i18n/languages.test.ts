/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  convertDetectedLanguage,
  normalizeInterfaceLanguage,
  toIntlLocale,
} from './languages'

describe('interface language locale conversion', () => {
  test('keeps the internal Chinese codes separate from Intl locale tags', () => {
    assert.equal(normalizeInterfaceLanguage('zh-CN'), 'zhCN')
    assert.equal(normalizeInterfaceLanguage('zh-TW'), 'zhTW')
    assert.equal(toIntlLocale('zhCN'), 'zh-CN')
    assert.equal(toIntlLocale('zhTW'), 'zh-TW')
  })

  test('returns a safe fallback for malformed Intl locale values', () => {
    assert.equal(toIntlLocale('zh_CN'), undefined)
    assert.equal(toIntlLocale('???'), undefined)
    assert.equal(toIntlLocale(null), undefined)
  })
})

test('regional browser locales keep their supported interface language', () => {
  for (const [input, expected] of [
    ['fr-FR', 'fr'],
    ['fr_CA', 'fr'],
    ['ja-JP', 'ja'],
    ['ru-RU', 'ru'],
    ['vi-VN', 'vi'],
    ['en-GB', 'en'],
  ]) {
    assert.equal(normalizeInterfaceLanguage(input), expected)
  }
  assert.equal(convertDetectedLanguage('zhTW'), 'zhTW')
  assert.equal(convertDetectedLanguage('zhCN'), 'zhCN')
  assert.equal(convertDetectedLanguage('zh-Hant-HK'), 'zhTW')
})
