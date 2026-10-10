/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { test } from 'node:test'

import { buildRequestBody, buildRequestSnippet } from './request-snippet'

test('request bodies follow each protocol', () => {
  assert.deepEqual(buildRequestBody('anthropic', 'model', 'hello'), {
    model: 'model',
    max_tokens: 512,
    messages: [{ role: 'user', content: 'hello' }],
  })
  assert.deepEqual(buildRequestBody('gemini', 'model', 'hello'), {
    contents: [{ parts: [{ text: 'hello' }] }],
  })
  assert.deepEqual(buildRequestBody('openai', 'model', 'hello'), {
    model: 'model',
    messages: [{ role: 'user', content: 'hello' }],
  })
})

test('cURL examples preserve apostrophes and shell syntax as literal prompt text', () => {
  const body = buildRequestBody(
    'openai',
    'model',
    'It\'s a test: $(exit 9), `exit 8`, "你好"'
  )
  const url = 'https://example.test/v1/chat/completions'
  const snippet = buildRequestSnippet('curl', url, body)
  const args = execFileSync(
    'sh',
    ['-c', `curl() { printf '%s\\n' "$@"; }\n${snippet}`],
    {
      encoding: 'utf8',
      env: { LMM_API_KEY: 'test-placeholder' },
    }
  )
    .trim()
    .split('\n')
  assert.equal(args[0], url)
  assert.equal(args[2], 'Authorization: Bearer test-placeholder')
  assert.deepEqual(JSON.parse(args.at(-1) ?? ''), body)
})
