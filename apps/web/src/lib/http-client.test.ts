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

import { QueryClient } from '@tanstack/react-query'
import type { AxiosAdapter, AxiosResponse } from 'axios'

import {
  bindAuthCache,
  setDevelopmentAuthRefreshAdapter,
} from '@/lib/auth-session'
import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

import { api } from './http-client'

const originalAPIAdapter = api.defaults.adapter

function response(
  config: Parameters<AxiosAdapter>[0],
  status: number,
  data: unknown
): AxiosResponse {
  return {
    config,
    data,
    headers: {},
    status,
    statusText: status === 200 ? 'OK' : 'Unauthorized',
  }
}

function bundle(token: string, expiresAt: number): AuthBundle {
  return {
    access_token: token,
    token_type: 'Bearer',
    access_expires_at: expiresAt,
    user: {
      id: 42,
      username: 'refresh-test',
      role: 1,
      developer_access_granted: true,
    },
    session: {
      sid: 'refresh-session',
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'test',
      created_at: 1,
      last_active_at: 1,
      expires_at: expiresAt + 600,
    },
  }
}

afterEach(() => {
  api.defaults.adapter = originalAPIAdapter
  useAuthStore.getState().auth.reset('idle')
})

describe('authenticated HTTP requests', () => {
  test('refreshes an expiring token before protected requests fan out', async () => {
    const now = Math.floor(Date.now() / 1000)
    const refreshed = bundle('fresh-token', now + 600)
    useAuthStore.getState().auth.setBundle(bundle('expiring-token', now + 10))
    let refreshCalls = 0
    let refreshTimeout = 0
    setDevelopmentAuthRefreshAdapter(async (config) => {
      refreshCalls += 1
      refreshTimeout = Number(config.timeout)
      return response(config, 200, { success: true, data: refreshed })
    })
    const authorizations: string[] = []
    api.defaults.adapter = async (config) => {
      authorizations.push(String(config.headers.Authorization ?? ''))
      return response(config, 200, { success: true, data: [] })
    }

    await Promise.all([
      api.get('/api/user/models'),
      api.get('/api/user/groups'),
      api.get('/api/user/2fa/status'),
    ])

    assert.equal(refreshCalls, 1)
    assert.equal(refreshTimeout, 10_000)
    assert.deepEqual(authorizations, [
      'Bearer fresh-token',
      'Bearer fresh-token',
      'Bearer fresh-token',
    ])
  })

  test('does not send a newly expired token after a transient refresh failure', async () => {
    const originalDateNow = Date.now
    let nowMs = 1_000_000
    Date.now = () => nowMs
    useAuthStore
      .getState()
      .auth.setBundle(bundle('nearly-expired-token', nowMs / 1000 + 1))
    setDevelopmentAuthRefreshAdapter(async (config) => {
      nowMs += 2_000
      return response(config, 503, { success: false })
    })
    let protectedCalls = 0
    api.defaults.adapter = async (config) => {
      protectedCalls += 1
      return response(config, 200, { success: true, data: [] })
    }

    try {
      await assert.rejects(
        api.get('/api/user/2fa/status', { skipErrorHandler: true }),
        (error: unknown) => {
          const requestError = error as {
            config?: { skipErrorHandler?: boolean }
          }
          assert.equal(requestError.config?.skipErrorHandler, true)
          return true
        }
      )
      assert.equal(protectedCalls, 0)
    } finally {
      Date.now = originalDateNow
    }
  })

  test('does not send protected requests after refresh rejects the session', async () => {
    const now = Math.floor(Date.now() / 1000)
    useAuthStore.getState().auth.setBundle(bundle('expired-token', now - 1))
    const queryClient = new QueryClient()
    queryClient.setQueryData(['assistant-status'], {
      developer_access_granted: true,
    })
    const unbind = bindAuthCache(queryClient)
    setDevelopmentAuthRefreshAdapter(async (config) =>
      response(config, 401, { success: false })
    )
    let protectedCalls = 0
    api.defaults.adapter = async (config) => {
      protectedCalls += 1
      return response(config, 200, { success: true, data: [] })
    }

    try {
      await assert.rejects(api.get('/api/user/models'), Error)
      assert.equal(protectedCalls, 0)
      assert.equal(queryClient.getQueryCache().getAll().length, 0)
      assert.equal(useAuthStore.getState().auth.user, null)
    } finally {
      unbind()
    }
  })
})
