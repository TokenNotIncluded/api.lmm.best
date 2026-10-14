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

import { api } from '@/lib/api'

import { createOAuthFlow, executeLogout, startWechatLogin } from './api'

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
    assert.deepEqual(
      await executeLogout({
        getExpectedSID: () => 'session-a',
        request: async () => ({ success: false, message: 'not revoked' }),
      }),
      { success: false, message: 'not revoked' }
    )
  })

  test('a cookie mismatch is rejected without hidden refresh or logout replay', async () => {
    const requestedSIDs: Array<string | undefined> = []
    const error = mismatchError()
    await assert.rejects(
      executeLogout({
        getExpectedSID: () => 'session-a',
        request: async (sid) => {
          requestedSIDs.push(sid)
          throw error
        },
      }),
      (actual) => actual === error
    )
    assert.deepEqual(requestedSIDs, ['session-a'])
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
