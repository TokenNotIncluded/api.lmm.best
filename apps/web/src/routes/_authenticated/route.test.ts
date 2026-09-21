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
import { afterEach, describe, test } from 'node:test'

import { isRedirect } from '@tanstack/react-router'

import { useAuthStore, type AuthUser } from '@/stores/auth-store'

import { Route } from './route'

function authenticate(user: AuthUser) {
  useAuthStore.getState().auth.setBundle({
    access_token: 'route-test-token',
    token_type: 'Bearer',
    access_expires_at: 1_900_000_000,
    user,
    session: {
      sid: 'route-test-session',
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'route-test',
      created_at: 1,
      last_active_at: 1,
      expires_at: 1_900_000_000,
    },
  })
}

async function runBeforeLoad(pathname: string) {
  let thrown: unknown
  try {
    await Route.options.beforeLoad?.({
      location: { href: pathname, pathname },
    } as never)
  } catch (error) {
    thrown = error
  }
  return thrown
}

afterEach(() => useAuthStore.getState().auth.reset('complete'))

describe('authenticated route access', () => {
  test('keeps a mobile L0 account on getting started and redirects other console routes there', async () => {
    authenticate({
      id: 7,
      username: 'mobile-l0',
      role: 1,
      developer_access_granted: false,
    })

    assert.equal(await runBeforeLoad('/getting-started'), undefined)
    assert.equal(await runBeforeLoad('/wallet'), undefined)
    const dashboardRedirect = await runBeforeLoad('/dashboard')
    assert.ok(isRedirect(dashboardRedirect))
    assert.equal(dashboardRedirect.options.to, '/getting-started')
  })

  test('lets an existing Persona E user reach dashboard, todos, and getting started', async () => {
    authenticate({
      id: 8,
      username: 'persona-e',
      role: 1,
      developer_access_granted: true,
    })

    for (const pathname of ['/dashboard', '/todos', '/getting-started']) {
      assert.equal(await runBeforeLoad(pathname), undefined)
    }
  })
})
