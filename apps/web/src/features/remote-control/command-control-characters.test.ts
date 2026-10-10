/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { createRemoteCommand } from './commands'

test('remote text rejects every unsupported ASCII control', () => {
  for (const code of [...Array.from({ length: 32 }, (_, code) => code), 127]) {
    if ([9, 10, 13].includes(code)) continue
    assert.throws(
      () =>
        createRemoteCommand({
          action: 'ui_input',
          request_id: 'question_123',
          text: 'a' + String.fromCodePoint(code) + 'b',
        }),
      /Invalid text input/
    )
  }
})

test('remote text retains tabs, newlines and ordinary Unicode', () => {
  for (const text of [
    'hello',
    '中文',
    'a' + String.fromCodePoint(9) + 'b',
    'a' + String.fromCodePoint(10) + 'b',
    'a' + String.fromCodePoint(13) + 'b',
  ]) {
    const command = createRemoteCommand({
      action: 'ui_input',
      request_id: 'question_123',
      text,
    })
    assert.equal(command.action, 'ui_input')
    assert.ok('text' in command)
    assert.equal(command.text, text)
  }
})
