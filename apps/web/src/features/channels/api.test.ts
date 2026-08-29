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
import { afterEach, test } from 'node:test'

import { api } from '@/lib/api'

import {
  type ChannelBalanceRefreshResponse,
  updateAllChannelsBalance,
} from './api'

const originalGet = api.get

afterEach(() => {
  api.get = originalGet
})

const fullFailure: ChannelBalanceRefreshResponse = {
  success: false,
  degraded: true,
  message: 'channel balance refresh failed',
  data: {
    attempted: 2,
    updated: 0,
    failed: 2,
    failures: [
      {
        channel_id: 7,
        code: 'provider_error',
        message: 'provider balance refresh failed',
      },
    ],
    failures_omitted: 1,
  },
}

test('updateAllChannelsBalance preserves the summary from an HTTP 502 envelope', async () => {
  api.get = (async () => {
    throw Object.assign(new Error('Request failed with status code 502'), {
      isAxiosError: true,
      response: { status: 502, data: fullFailure },
    })
  }) as typeof api.get

  assert.deepEqual(await updateAllChannelsBalance(), fullFailure)
})

test('updateAllChannelsBalance rethrows HTTP errors without a valid summary', async () => {
  const invalidError = Object.assign(new Error('network failure'), {
    isAxiosError: true,
    response: { status: 502, data: { success: false, message: 'failed' } },
  })
  api.get = (async () => {
    throw invalidError
  }) as typeof api.get

  await assert.rejects(updateAllChannelsBalance(), invalidError)
})
