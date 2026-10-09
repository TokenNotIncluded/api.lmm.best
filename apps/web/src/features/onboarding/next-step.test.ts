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

import type { AuthUser } from '@/stores/auth-store'

import { getAccountNextStep } from './next-step'

const user: AuthUser = { id: 1, username: 'new-user', role: 1 }
test('L0 guidance uses the current access decision, not old applications', () => {
  assert.equal(getAccountNextStep(null).to, '/sign-in')
  assert.deepEqual(getAccountNextStep(user), {
    to: '/getting-started',
    label: 'Enable L1 access',
  })
})
test('OAuth and undecided users are not required to create manual keys', () => {
  const approved = {
    ...user,
    developer_access_granted: true,
    onboarding: {
      activation_complete: true,
      credential_complete: true,
      api_key_created: false,
      first_request_complete: false,
      stage: 'first_request' as const,
    },
  }
  assert.equal(getAccountNextStep(approved, 'oauth').to, '/guide')
  assert.equal(getAccountNextStep(approved).label, 'Choose your client')
  assert.equal(getAccountNextStep(approved, 'api-key').to, '/keys')
  assert.equal(
    getAccountNextStep({
      ...approved,
      onboarding: { ...approved.onboarding, first_request_complete: true },
    }).to,
    '/dashboard'
  )
})
