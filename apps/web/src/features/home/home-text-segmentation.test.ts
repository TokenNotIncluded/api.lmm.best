/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { segmentMovingText } from './home-text-segmentation'

test('descriptions keep their text and grapheme boundaries across scripts', () => {
  for (const [language, text] of [
    ['zh', '模型价格，开始接入。'],
    ['zhCN', '模型价格，开始接入。'],
    ['en', 'Choose a model.'],
    ['ja', 'モデルを選ぶ。'],
    ['fr', 'Choisissez un modèle.'],
    ['ru', 'Выберите модель.'],
    ['vi', 'Chọn mô hình.'],
  ]) {
    const parts = segmentMovingText(text, language)
    assert.equal(parts.map((part) => part.word).join(''), text)
    assert.equal(parts.map((part) => part.letters.join('')).join(''), text)
  }
  const accent = segmentMovingText('e\u0301', 'fr')
  assert.deepEqual(accent[0].letters, ['e\u0301'])
})

test('animated words keep closing punctuation off the start of a wrapped line', () => {
  for (const [language, text] of [
    ['zhCN', '共用一个 API。选好模型，接入应用。'],
    ['ja', 'モデルを選ぶ。接続する。'],
    ['en', 'Choose a model. Start here!'],
    ['fr', 'Choisissez un modèle.'],
    ['ru', 'Выберите модель.'],
  ]) {
    const parts = segmentMovingText(text, language)
    assert.equal(parts.map((part) => part.word).join(''), text)
    assert.equal(parts.map((part) => part.letters.join('')).join(''), text)
    assert.ok(
      parts.every(
        (part) => !/^[\p{Pe}\p{Pf},.!?;:，。！？；：、…]+$/u.test(part.word)
      )
    )
  }
})
