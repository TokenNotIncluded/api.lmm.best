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
import { AxiosError, AxiosHeaders } from 'axios'

import {
  bootstrapAuthentication,
  setDevelopmentAuthRefreshAdapter,
} from '@/lib/auth-session'
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
  test('keeps a cold profile request retryable when checking the session fails', async () => {
    for (const status of [0, 500, 503]) {
      useAuthStore.getState().auth.reset('idle')
      setDevelopmentAuthRefreshAdapter(async (config) => {
        throw new AxiosError(
          'Authentication transport unavailable',
          status === 0 ? AxiosError.ERR_NETWORK : AxiosError.ERR_BAD_RESPONSE,
          config,
          undefined,
          status === 0
            ? undefined
            : {
                config,
                data: { success: false },
                headers: new AxiosHeaders(),
                status,
                statusText: 'Unavailable',
              }
        )
      })
      assert.equal((await bootstrapAuthentication()).kind, 'transient_error')
      assert.equal(useAuthStore.getState().auth.bootstrapState, 'idle')

      const error = await runBeforeLoad('/profile')
      assert.ok(error instanceof Error, `status ${status} must offer retry`)
      assert.equal(isRedirect(error), false)
      assert.equal(useAuthStore.getState().auth.user, null)
    }
  })

  test('does not redirect a pending session check to sign in', async () => {
    useAuthStore.getState().auth.reset('checking')
    const error = await runBeforeLoad('/profile')
    assert.ok(error instanceof Error)
    assert.equal(isRedirect(error), false)
  })

  test('still sends an authoritatively unauthenticated profile request to sign in', async () => {
    useAuthStore.getState().auth.reset('idle')
    setDevelopmentAuthRefreshAdapter(async (config) => ({
      config,
      data: { success: false, code: 'AUTH_UNAUTHORIZED' },
      headers: new AxiosHeaders(),
      status: 401,
      statusText: 'Unauthorized',
    }))
    assert.equal((await bootstrapAuthentication()).kind, 'anonymous')
    assert.equal(useAuthStore.getState().auth.bootstrapState, 'complete')
    const redirect = await runBeforeLoad('/profile')
    assert.ok(isRedirect(redirect))
    assert.equal(redirect.options.to, '/sign-in')
  })

  test('keeps an L0 account on wallet and redirects restricted console routes there', async () => {
    authenticate({
      id: 7,
      username: 'mobile-l0',
      role: 1,
      developer_access_granted: false,
    })

    assert.equal(await runBeforeLoad('/wallet'), undefined)
    const oldPageRedirect = await runBeforeLoad('/getting-started')
    assert.ok(isRedirect(oldPageRedirect))
    assert.equal(oldPageRedirect.options.to, '/wallet')
    const dashboardRedirect = await runBeforeLoad('/dashboard')
    assert.ok(isRedirect(dashboardRedirect))
    assert.equal(dashboardRedirect.options.to, '/wallet')
  })

  test('lets an existing Persona E user reach dashboard, todos, and wallet', async () => {
    authenticate({
      id: 8,
      username: 'persona-e',
      role: 1,
      developer_access_granted: true,
    })

    for (const pathname of ['/dashboard', '/todos', '/wallet']) {
      assert.equal(await runBeforeLoad(pathname), undefined)
    }
  })
})
