import assert from 'node:assert/strict'
import { test } from 'node:test'

import { logRecovery, safeLogDiagnostic } from './recovery'

test('does not infer money or upstream blame from a generic 429', () => {
  assert.match(logRecovery({ status_code: 429 }).message, /does not identify/)
  assert.equal(
    logRecovery({ status_code: 429, error_code: 'insufficient_user_quota' })
      .target,
    'plan'
  )
  assert.equal(
    logRecovery({
      status_code: 403,
      error_code: 'pre_consume_token_quota_failed',
    }).target,
    'api-key'
  )
})

test('removes credentials in JSON, headers, and URL parameters before copying', () => {
  const result = safeLogDiagnostic(
    '{"api_key":"secret-value", "password":"hidden-pass", "prompt":"private message"}\nCookie: session=private-cookie\nhttps://name:pass@example.com/v1?token=private-token&email=a@b.com\nBearer abc-secret-token'
  )
  for (const secret of [
    'secret-value',
    'hidden-pass',
    'private message',
    'private-cookie',
    'private-token',
    'abc-secret-token',
    'name:pass',
  ]) {
    assert.equal(result.includes(secret), false, secret)
  }
  assert.equal(result, '[UNSTRUCTURED_ERROR_OMITTED]')
})

test('retains allowlisted metadata without copying arbitrary error bodies', () => {
  const result = JSON.parse(
    safeLogDiagnostic(
      JSON.stringify({
        request_id: 'req_abc123',
        status_code: 404,
        model: 'my-model',
        messages: [{ content: 'private' }],
        error: {
          code: 'not_found',
          message: 'private body',
          unexpected: 'opaque credential',
        },
      })
    )
  )
  assert.deepEqual(result, {
    request_id: 'req_abc123',
    status_code: 404,
    model: 'my-model',
    error: { code: 'not_found' },
  })
})

test('omits multiline and malformed bodies, opaque tokens and URL credentials', () => {
  for (const input of [
    '{ "messages": [\n{"content":"private"}\n]}',
    '{"request_body": [\n"secret"',
    'opaque-non-sk-credential',
    'https://user:pass@host/token-in-path?token=secret',
  ]) {
    assert.equal(safeLogDiagnostic(input), '[UNSTRUCTURED_ERROR_OMITTED]')
  }
})
