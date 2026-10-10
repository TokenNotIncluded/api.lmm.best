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
import { describe, test } from 'node:test'

import { api, type RefreshOutcome } from '@/lib/api'
import type { AuthBundle } from '@/stores/auth-store'

import { createOAuthFlow, executeLogout, startWechatLogin } from './api'

const bundle: AuthBundle = {
  access_token: 'access-token',
  token_type: 'Bearer',
  access_expires_at: 1_900_000_000,
  user: { id: 1, username: 'test-user', role: 1 },
  session: {
    sid: 'session-b',
    current: true,
    login_method: 'password',
    ip: '127.0.0.1',
    user_agent: 'test',
    created_at: 1,
    last_active_at: 1,
    expires_at: 1_900_000_000,
  },
}

function mismatchError() {
  return {
    isAxiosError: true,
    response: {
      status: 409,
      data: { code: 'AUTH_SESSION_MISMATCH' },
    },
  }
}

describe('logout coordination', () => {
  test('returns an unsuccessful response without pretending to sign out', async () => {
    let refreshCount = 0
    const result = await executeLogout({
      getExpectedSID: () => 'session-a',
      request: async () => ({ success: false, message: 'not revoked' }),
      refresh: async () => {
        refreshCount += 1
        return { kind: 'anonymous' }
      },
    })

    assert.deepEqual(result, { success: false, message: 'not revoked' })
    assert.equal(refreshCount, 0)
  })

  test('recovers a cookie mismatch and retries with the refreshed SID', async () => {
    let sid = 'session-a'
    const requestedSIDs: Array<string | undefined> = []
    const result = await executeLogout({
      getExpectedSID: () => sid,
      request: async (expectedSID) => {
        requestedSIDs.push(expectedSID)
        if (requestedSIDs.length === 1) throw mismatchError()
        return { success: true, message: '' }
      },
      refresh: async () => {
        sid = bundle.session.sid
        return { kind: 'authenticated', bundle }
      },
    })

    assert.deepEqual(result, { success: true, message: '' })
    assert.deepEqual(requestedSIDs, ['session-a', 'session-b'])
  })

  test('treats a mismatch that refresh confirms anonymous as signed out', async () => {
    const result = await executeLogout({
      getExpectedSID: () => 'session-a',
      request: async () => {
        throw mismatchError()
      },
      refresh: async () => ({ kind: 'anonymous' }),
    })

    assert.deepEqual(result, { success: true, message: '' })
  })

  test('preserves the active session when mismatch recovery is temporary', async () => {
    const originalError = mismatchError()
    const transient: RefreshOutcome = {
      kind: 'transient_error',
      error: new Error('offline'),
    }

    await assert.rejects(
      executeLogout({
        getExpectedSID: () => 'session-a',
        request: async () => {
          throw originalError
        },
        refresh: async () => transient,
      }),
      (error) => error === originalError
    )
  })

  test('treats an already anonymous or legacy 404 logout endpoint as signed out', async () => {
    for (const status of [401, 404]) {
      const result = await executeLogout({
        getExpectedSID: () => 'session-a',
        request: async () => {
          throw {
            isAxiosError: true,
            response: { status, data: { code: 'AUTH_NOT_FOUND' } },
          }
        },
        refresh: async () => ({ kind: 'transient_error', error: new Error() }),
      })

      assert.deepEqual(result, { success: true, message: '' })
    }
  })
})

describe('OAuth flow initialization', () => {
  test('carries the stored invitation into the browser-bound WeChat flow', async () => {
    const originalPost = api.post
    const originalWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
    const requests: Array<{ body: unknown; config: unknown }> = []
    let affiliateCode: string | null = 'inviter-code'
    Object.defineProperty(globalThis, 'window', {
      configurable: true,
      value: {
        localStorage: {
          getItem: (key: string) => {
            assert.equal(key, 'aff')
            return affiliateCode
          },
        },
      },
    })
    api.post = (async (url: string, body: unknown, config: unknown) => {
      assert.equal(url, '/api/oauth/wechat/start')
      requests.push({ body, config })
      return {
        data: { success: true, data: { flow_token: 'wechat-flow-token' } },
      }
    }) as typeof api.post

    try {
      assert.equal(await startWechatLogin(true), 'wechat-flow-token')
      affiliateCode = null
      await startWechatLogin()
      assert.deepEqual(requests, [
        {
          body: { accepted_legal: true, aff: 'inviter-code' },
          config: { skipAuthRefresh: true },
        },
        {
          body: { accepted_legal: false, aff: undefined },
          config: { skipAuthRefresh: true },
        },
      ])
    } finally {
      api.post = originalPost
      if (originalWindow) {
        Object.defineProperty(globalThis, 'window', originalWindow)
      } else {
        Reflect.deleteProperty(globalThis, 'window')
      }
    }
  })

  test('sends legal consent for login flows', async () => {
    const originalPost = api.post
    let requestBody: unknown
    api.post = (async (url: string, body: unknown) => {
      assert.equal(url, '/api/oauth/state')
      requestBody = body
      return {
        data: {
          success: true,
          data: { flow_token: 'oauth-flow-token' },
        },
      }
    }) as typeof api.post

    try {
      const token = await createOAuthFlow('linuxdo', 'login', true)
      assert.equal(token, 'oauth-flow-token')
      assert.deepEqual(requestBody, {
        provider: 'linuxdo',
        intent: 'login',
        aff: undefined,
        accepted_legal: true,
      })
    } finally {
      api.post = originalPost
    }
  })

  test('does not attach registration consent to binding flows', async () => {
    const originalPost = api.post
    let requestBody: unknown
    api.post = (async (_url: string, body: unknown) => {
      requestBody = body
      return {
        data: {
          success: true,
          data: { flow_token: 'oauth-bind-token' },
        },
      }
    }) as typeof api.post

    try {
      await createOAuthFlow('github', 'bind', true)
      assert.deepEqual(requestBody, {
        provider: 'github',
        intent: 'bind',
        aff: undefined,
        accepted_legal: undefined,
      })
    } finally {
      api.post = originalPost
    }
  })
})
