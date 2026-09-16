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

import {
  ASSISTANT_MAX_REQUEST_ATTEMPTS,
  archiveAssistantConversation,
  sendAssistantMessage,
  submitAssistantAccountDisableRequest,
  submitAssistantAdminChange,
  unarchiveAssistantConversation,
} from './api'

type AssistantPostConfig = {
  headers?: Record<string, string>
  skipBusinessError?: boolean
  skipErrorHandler?: boolean
}

type AssistantPostCall = {
  url: string
  data: unknown
  config: AssistantPostConfig | undefined
}

function assistantAxiosError(status?: number) {
  return Object.assign(new Error(status ? `HTTP ${status}` : 'network down'), {
    isAxiosError: true,
    ...(status === undefined ? {} : { response: { status } }),
  })
}

function assistantResponse(content = 'ready') {
  return {
    data: { choices: [{ message: { content } }] },
    headers: { 'x-lmm-assistant-intent': 'other' },
  }
}

async function withAssistantPost<T>(
  post: (url: string, data: unknown, config: unknown) => Promise<unknown>,
  run: () => Promise<T>
): Promise<T> {
  const originalPost = api.post
  api.post = post as typeof api.post
  try {
    return await run()
  } finally {
    api.post = originalPost
  }
}

function assistantAttempt(config: unknown): string | undefined {
  return (config as AssistantPostConfig | undefined)?.headers?.[
    'X-LMM-Assistant-Attempt'
  ]
}

describe('assistant automatic retry policy', () => {
  test('retries transient HTTP statuses with the same chat payload', async () => {
    for (const status of [408, 425, 429, 500, 502, 503, 599]) {
      const calls: AssistantPostCall[] = []

      const reply = await withAssistantPost(
        async (url, data, rawConfig) => {
          const config = rawConfig as AssistantPostConfig | undefined
          calls.push({ url, data, config })
          if (calls.length === 1) throw assistantAxiosError(status)
          return assistantResponse()
        },
        () => sendAssistantMessage('hello')
      )

      assert.equal(reply.content, 'ready')
      assert.equal(calls.length, 2, `status ${status}`)
      assert.deepEqual(
        calls.map((call) => call.url),
        ['/api/assistant/chat', '/api/assistant/chat']
      )
      assert.deepEqual(calls[0]?.data, calls[1]?.data)
      assert.deepEqual(
        calls.map((call) => assistantAttempt(call.config)),
        ['1', '2']
      )
      assert.equal(calls[0]?.config?.skipBusinessError, true)
      assert.equal(calls[0]?.config?.skipErrorHandler, true)
    }
  })

  test('does not replay a request with an unknown transport outcome', async () => {
    let callCount = 0
    const expected = assistantAxiosError()
    await withAssistantPost(
      async () => {
        callCount += 1
        throw expected
      },
      async () => {
        await assert.rejects(
          () => sendAssistantMessage('hello'),
          (error) => error === expected
        )
      }
    )
    assert.equal(callCount, 1)
  })

  test('does not retry non-retryable HTTP 4xx responses', async () => {
    for (const status of [400, 401, 403, 404, 409, 422, 499]) {
      const expectedError = assistantAxiosError(status)
      const attempts: Array<string | undefined> = []
      let callCount = 0

      await withAssistantPost(
        async (_url, _data, config) => {
          callCount += 1
          attempts.push(assistantAttempt(config))
          throw expectedError
        },
        async () => {
          await assert.rejects(
            () => sendAssistantMessage('hello'),
            (error) => error === expectedError
          )
        }
      )

      assert.equal(callCount, 1, `status ${status}`)
      assert.deepEqual(attempts, ['1'])
    }
  })

  test('stops at the configured maximum total attempts', async () => {
    const attempts: Array<string | undefined> = []
    let callCount = 0
    let lastError: Error | undefined

    await withAssistantPost(
      async (_url, _data, config) => {
        callCount += 1
        attempts.push(assistantAttempt(config))
        lastError = assistantAxiosError(503)
        throw lastError
      },
      async () => {
        await assert.rejects(
          () => sendAssistantMessage('hello'),
          (error) => error === lastError
        )
      }
    )

    assert.equal(callCount, ASSISTANT_MAX_REQUEST_ATTEMPTS)
    assert.deepEqual(attempts, ['1', '2'])
  })
})

describe('assistant retry write boundary', () => {
  test('does not automatically replay confirmation-gated write endpoints', async () => {
    const writes = [
      {
        name: 'account disable request',
        url: '/api/user/account-action-requests',
        run: () =>
          submitAssistantAccountDisableRequest({
            target_user_id: 7,
            reason: 'confirmed account safety issue',
            confirmation_token: 'confirmation-token',
          }),
      },
      {
        name: 'administrator change',
        url: '/api/assistant/admin/apply',
        run: () => submitAssistantAdminChange('confirmation-token'),
      },
      {
        name: 'archive conversation',
        url: '/api/assistant/conversations/7/archive',
        run: () => archiveAssistantConversation(7),
      },
      {
        name: 'unarchive conversation',
        url: '/api/assistant/conversations/7/unarchive',
        run: () => unarchiveAssistantConversation(7),
      },
    ]

    for (const write of writes) {
      const calls: string[] = []
      await withAssistantPost(
        async (url) => {
          calls.push(url)
          throw assistantAxiosError(503)
        },
        async () => {
          await assert.rejects(write.run)
        }
      )

      assert.deepEqual(calls, [write.url], write.name)
    }
  })
})
