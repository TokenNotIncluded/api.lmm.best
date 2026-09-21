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

import { api } from '@/lib/api'

import { getProfileUsageWindow, performCheckin } from './api'

const originalGet = api.get
const originalPost = api.post

afterEach(() => {
  api.get = originalGet
  api.post = originalPost
})

describe('profile activity API', () => {
  test('requests one bounded self-usage window without global error toasts', async () => {
    let request: { url: string; config: unknown } | null = null
    const rows = [{ created_at: 1_700_000_000, token_used: 42, count: 2 }]

    api.get = (async (url, config) => {
      request = { url, config }
      return { data: { success: true, data: rows } }
    }) as typeof api.get

    assert.deepEqual(
      await getProfileUsageWindow({
        start_timestamp: 1_700_000_000,
        end_timestamp: 1_700_000_100,
      }),
      rows
    )
    assert.deepEqual(request, {
      url: '/api/data/self',
      config: {
        params: {
          start_timestamp: 1_700_000_000,
          end_timestamp: 1_700_000_100,
          default_time: 'day',
        },
        skipBusinessError: true,
        skipErrorHandler: true,
      },
    })
  })
})

describe('profile check-in API', () => {
  test('suppresses the global business toast for an explicit Turnstile result', async () => {
    let request: {
      url: string
      config: { skipBusinessError?: boolean } | undefined
    } | null = null

    api.post = (async (url, _data, config) => {
      request = { url, config }
      return { data: { success: false, message: 'Turnstile 校验失败' } }
    }) as typeof api.post

    await performCheckin('turnstile-token')

    assert.deepEqual(request, {
      url: '/api/user/checkin?turnstile=turnstile-token',
      config: { skipBusinessError: true },
    })
  })
})
