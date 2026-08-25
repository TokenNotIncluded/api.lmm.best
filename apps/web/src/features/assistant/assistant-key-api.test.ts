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

import { AxiosError, AxiosHeaders } from 'axios'

import { api } from '@/lib/api'

import {
  AssistantRequestError,
  confirmAssistantDefaultKey,
  prepareAssistantDefaultKey,
} from './api'

const originalPost = api.post

afterEach(() => {
  api.post = originalPost
})

describe('assistant key API wire contract', () => {
  test('prepare sends mutable draft fields to the prepare endpoint only', async () => {
    const calls: Array<{ url: string; data: unknown }> = []
    api.post = (async (url: string, data: unknown) => {
      calls.push({ url, data })
      return {
        data: {
          success: true,
          data: {
            type: 'create_key',
            confirmation_token: 'opaque-token',
            requires_confirmation: true,
            expires_in_seconds: 600,
            name: 'prepared-name',
            group: 'default',
          },
        },
      }
    }) as typeof api.post

    await prepareAssistantDefaultKey('prepared-name', 'default', 2)

    assert.deepEqual(calls, [
      {
        url: '/api/assistant/tools/prepare-key',
        data: {
          name: 'prepared-name',
          group: 'default',
          group_warning_confirmations: 2,
        },
      },
    ])
  })

  test('confirm sends only the opaque token and normalized 2FA code', async () => {
    const calls: Array<{ url: string; data: unknown }> = []
    api.post = (async (url: string, data: unknown) => {
      calls.push({ url, data })
      return {
        data: {
          success: true,
          data: {
            id: 7,
            name: 'server-owned-name',
            group: 'server-owned-group',
            expired_time: -1,
            card: { id: 'secure-card', label: 'Private API key' },
          },
        },
      }
    }) as typeof api.post

    await confirmAssistantDefaultKey('opaque-token', '  ABCD-EFGH  ')

    assert.deepEqual(calls, [
      {
        url: '/api/assistant/tools/create-key',
        data: {
          confirmation_token: 'opaque-token',
          two_factor_code: 'ABCD-EFGH',
        },
      },
    ])
    const confirmBody = calls[0]?.data as Record<string, unknown>
    assert.equal('name' in confirmBody, false)
    assert.equal('group' in confirmBody, false)
    assert.equal('confirmed' in confirmBody, false)
  })

  test('preserves authoritative confirmation error codes from HTTP failures', async () => {
    api.post = (async () => {
      const error = new AxiosError('unprocessable confirmation')
      error.response = {
        data: {
          success: false,
          message: 'The confirmation is no longer valid.',
          code: 'ASSISTANT_KEY_CONFIRMATION_INVALID',
        },
        status: 422,
        statusText: 'Unprocessable Entity',
        headers: {},
        config: { headers: new AxiosHeaders() },
      }
      throw error
    }) as typeof api.post

    await assert.rejects(
      () => confirmAssistantDefaultKey('expired-token'),
      (error: unknown) =>
        error instanceof AssistantRequestError &&
        error.code === 'ASSISTANT_KEY_CONFIRMATION_INVALID'
    )
  })
})
