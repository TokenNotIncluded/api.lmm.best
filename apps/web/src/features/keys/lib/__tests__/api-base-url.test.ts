/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { resolveApiBaseUrl } from '../api-base-url'

test('uses configured public API address and includes /v1 exactly once', () => {
  for (const suffix of ['', '/', '/v1', '/v1/', '/v1/v1']) {
    assert.equal(
      resolveApiBaseUrl(
        `https://relay.example.test/gateway${suffix}`,
        'https://console.example.test'
      ),
      'https://relay.example.test/gateway/v1'
    )
  }
  assert.equal(
    resolveApiBaseUrl(undefined, 'https://console.example.test'),
    'https://console.example.test/v1'
  )
})

test('does not copy credentials, query secrets or invalid configured URLs', () => {
  for (const address of [
    'https://key:secret@relay.test',
    'https://relay.test?key=secret',
    'https://relay.test/#secret',
    'javascript:alert(1)',
    'invalid',
  ]) {
    assert.equal(
      resolveApiBaseUrl(address, 'https://console.example.test'),
      null
    )
  }
})
