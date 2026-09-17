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
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { loginSessionExpiresAt } from './login-session-expiry'

const createdAt = 1_700_000_000
const week = 7 * 24 * 60 * 60
const session = {
  created_at: createdAt,
  expires_at: createdAt + 30 * 24 * 60 * 60,
  last_active_at: createdAt + 6 * 24 * 60 * 60,
}

test('weekly auto sign-out is the default and uses the original login', () => {
  assert.equal(loginSessionExpiresAt(session), createdAt + week + 1)
})

test('disabled auto sign-out preserves the absolute expiry', () => {
  assert.equal(loginSessionExpiresAt(session, false), session.expires_at)
})

test('an earlier absolute expiry is never extended', () => {
  const shorter = { ...session, expires_at: createdAt + 60 }
  assert.equal(loginSessionExpiresAt(shorter, true), shorter.expires_at)
  assert.equal(loginSessionExpiresAt(shorter, false), shorter.expires_at)
})

test('refresh and recent activity cannot postpone auto sign-out', () => {
  const refreshed = { ...session, last_active_at: session.expires_at - 1 }
  assert.equal(loginSessionExpiresAt(refreshed), createdAt + week + 1)
})

test('the displayed second matches the strict backend age boundary', () => {
  const expiry = loginSessionExpiresAt(session)
  assert.equal(session.created_at < expiry - 1 - week, false)
  assert.equal(session.created_at < expiry - week, true)
})

test('policy changes affect the display without mutating session data', () => {
  const copy = { ...session }
  assert.equal(loginSessionExpiresAt(copy, false), copy.expires_at)
  assert.equal(loginSessionExpiresAt(copy, true), createdAt + week + 1)
  assert.deepEqual(copy, session)
})

test('invalid creation metadata does not invent a weekly deadline', () => {
  for (const created_at of [NaN, Infinity, -1, 1.5, Number.MAX_SAFE_INTEGER]) {
    const invalid = { ...session, created_at }
    assert.equal(loginSessionExpiresAt(invalid), invalid.expires_at)
  }
})
