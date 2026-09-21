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
