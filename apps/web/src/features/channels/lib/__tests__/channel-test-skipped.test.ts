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
/*
Copyright (C) 2026 LIghtJUNction
*/
import assert from 'node:assert/strict'
import { afterEach, beforeEach, test } from 'node:test'

import i18next from 'i18next'
import { toast } from 'sonner'

import { api, type ApiRequestConfig } from '@/lib/api'

import type { ChannelTestResponse } from '../../types'
import { handleTestChannel } from '../channel-actions'

await i18next.init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        '{{target}} test skipped': 'Skipped test for {{target}}',
        Skipped: 'No request sent',
      },
    },
  },
})

type TestComplete = NonNullable<Parameters<typeof handleTestChannel>[2]>
type ToastCall = { kind: 'info' | 'success' | 'error'; args: unknown[] }

const originals = {
  get: api.get,
  info: toast.info,
  success: toast.success,
  error: toast.error,
}
let notifications: ToastCall[] = []
let completions: Parameters<TestComplete>[] = []

const onTestComplete: TestComplete = (...args) => {
  completions.push(args)
}

function mockResponse(response: ChannelTestResponse) {
  const requests: { url: string; config?: ApiRequestConfig }[] = []
  api.get = (async (url: string, config?: ApiRequestConfig) => {
    requests.push({ url, config })
    return { data: response }
  }) as typeof api.get
  return requests
}

beforeEach(() => {
  notifications = []
  completions = []
  for (const kind of ['info', 'success', 'error'] as const) {
    toast[kind] = ((...args: unknown[]) => {
      notifications.push({ kind, args })
      return `${kind}-toast`
    }) as typeof toast.info
  }
})

afterEach(() => {
  api.get = originals.get
  toast.info = originals.info
  toast.success = originals.success
  toast.error = originals.error
})

test('skipped tests show an informational notice and omit stale latency and error codes', async () => {
  const message = 'Realtime testing requires an interactive connection'
  const requests = mockResponse({
    success: false,
    skipped: true,
    message,
    error_code: 'stale_error',
    time: 9,
    data: { response_time: 9000, error: 'stale failure' },
  })

  await handleTestChannel(
    62,
    {
      channelName: 'Realtime',
      testModel: 'gpt-realtime',
      endpointType: 'realtime',
      stream: true,
    },
    onTestComplete
  )

  assert.deepEqual(requests, [
    {
      url: '/api/channel/test/62',
      config: {
        params: {
          model: 'gpt-realtime',
          endpoint_type: 'realtime',
          stream: true,
        },
        skipBusinessError: true,
        skipErrorHandler: true,
      },
    },
  ])
  assert.deepEqual(completions, [[false, undefined, message, undefined, true]])
  assert.deepEqual(notifications, [
    {
      kind: 'info',
      args: [
        'Skipped test for Channel Realtime model gpt-realtime',
        { description: message },
      ],
    },
  ])
})

test('skipped tests without a message use the translated fallback and preserve non-streaming requests', async () => {
  const requests = mockResponse({ success: false, skipped: true })

  await handleTestChannel(
    7,
    { testModel: 'jev-preview', endpointType: 'systemone', stream: false },
    onTestComplete
  )

  assert.deepEqual(requests[0]?.config?.params, {
    model: 'jev-preview',
    endpoint_type: 'systemone',
  })
  assert.deepEqual(completions, [
    [false, undefined, 'No request sent', undefined, true],
  ])
  assert.deepEqual(notifications, [
    {
      kind: 'info',
      args: [
        'Skipped test for Model jev-preview',
        { description: 'No request sent' },
      ],
    },
  ])
})

test('silent skipped tests still report the skip through the callback', async () => {
  mockResponse({
    success: false,
    skipped: true,
    message: 'Test is unavailable',
  })

  await handleTestChannel(7, { silent: true }, onTestComplete)

  assert.deepEqual(completions, [
    [false, undefined, 'Test is unavailable', undefined, true],
  ])
  assert.deepEqual(notifications, [])
})

test('successful tests retain the original two-argument callback and success notice', async () => {
  mockResponse({ success: true, data: { response_time: 125 } })

  await handleTestChannel(7, undefined, onTestComplete)

  assert.deepEqual(completions, [[true, 125]])
  assert.deepEqual(notifications, [
    {
      kind: 'success',
      args: [
        'Channel test succeeded',
        { description: 'Response time: 125 ms' },
      ],
    },
  ])
})

test('failed tests retain latency and error code without a skipped callback argument', async () => {
  mockResponse({
    success: false,
    message: 'Provider rejected the request',
    error_code: 'provider_error',
    time: 0.25,
  })

  await handleTestChannel(7, undefined, onTestComplete)

  assert.deepEqual(completions, [
    [false, 250, 'Provider rejected the request', 'provider_error'],
  ])
  assert.deepEqual(notifications, [
    {
      kind: 'error',
      args: [
        'Channel test failed',
        { description: 'Provider rejected the request (provider_error)' },
      ],
    },
  ])
})
