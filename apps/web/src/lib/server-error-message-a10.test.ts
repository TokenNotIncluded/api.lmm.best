// SPDX-License-Identifier: AGPL-3.0-only
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { getServerErrorMessageKey } from './server-error-message'

const key = 'This server does not support registration with these options. No account was created.'

test('A02 registration refusal is translated for body, nested code and HTTP errors', () => {
  const data = { success: false, code: 'AUTH_REGISTRATION_FEATURE_UNAVAILABLE' }
  assert.equal(getServerErrorMessageKey(data), key)
  assert.equal(getServerErrorMessageKey({ error: { code: data.code } }), key)
  assert.equal(getServerErrorMessageKey({ response: { status: 503, data } }), key)
})

test('unknown server codes never access Object prototype properties', () => {
  for (const code of ['__proto__', 'constructor', 'toString', 'valueOf', 'hasOwnProperty', 'not-known']) {
    assert.equal(getServerErrorMessageKey({ code }), null)
  }
})

test('backend message text cannot become a translation key or alter the known refusal', () => {
  assert.equal(getServerErrorMessageKey({ message: '<script>synthetic</script>' }), null)
  assert.equal(getServerErrorMessageKey({ code: 'AUTH_REGISTRATION_FEATURE_UNAVAILABLE',
    message: 'synthetic misleading success' }), key)
  for (const value of [null, [], '', 0, { code: 1 }, { error: {} }]) {
    assert.equal(getServerErrorMessageKey(value), null)
  }
})

test('existing session and service error keys remain unchanged', () => {
  assert.equal(getServerErrorMessageKey({ code: 'service_temporarily_unavailable' }),
    'The service is temporarily unavailable. Please try again later.')
  assert.match(getServerErrorMessageKey({ code: 'AUTH_SESSION_LIMIT' }) ?? '', /Too many active login sessions/)
})
