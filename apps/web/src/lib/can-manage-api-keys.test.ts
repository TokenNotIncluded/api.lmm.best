/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { AuthUser } from '@/stores/auth-store'

import { canManageApiKeys } from './can-manage-api-keys'
import { isConsoleActivated, isContributorRoute } from './console-activation'

const user = (values: Partial<AuthUser> = {}): AuthUser => ({
  id: 7,
  username: 'example',
  role: 1,
  ...values,
})

test('a true server key capability grants only key management', () => {
  const account = user({
    developer_access_granted: false,
    permissions: { api_key_management: true },
  })
  assert.equal(canManageApiKeys(account), true)
  assert.equal(isConsoleActivated(account), false)
  assert.equal(isContributorRoute('/keys'), true)
  assert.equal(isContributorRoute('/channels'), false)
})
test('explicit false wins and absent capability retains the old access gate', () => {
  assert.equal(
    canManageApiKeys(
      user({
        developer_access_granted: true,
        permissions: { api_key_management: false },
      })
    ),
    false
  )
  assert.equal(canManageApiKeys(user({ developer_access_granted: true })), true)
  assert.equal(
    canManageApiKeys(user({ developer_access_granted: false })),
    false
  )
  assert.equal(canManageApiKeys(null), false)
  assert.equal(canManageApiKeys(undefined), false)
})
test('truthy malformed fields, balance, role and onboarding flags do not grant access', () => {
  for (const capability of [1, 'true', {}, []]) {
    assert.equal(
      canManageApiKeys(
        user({ permissions: { api_key_management: capability as boolean } })
      ),
      false
    )
  }
  assert.equal(
    canManageApiKeys(
      user({ role: 100, quota: 1000000, activation_complete: true })
    ),
    false
  )
})
