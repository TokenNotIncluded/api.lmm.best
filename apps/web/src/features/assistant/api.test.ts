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
  buildAssistantConversation,
  getAssistantConversationHistory,
  isAssistantRequestAborted,
  parseAssistantAction,
  parseAssistantIntent,
  parseAssistantReply,
  parseAssistantToolTraces,
  sendAssistantMessage,
  type AssistantChatMessage,
  unarchiveAssistantConversation,
} from './api'

function retryableAxiosError(status: number) {
  return Object.assign(new Error(`HTTP ${status}`), {
    isAxiosError: true,
    response: { status },
  })
}

describe('assistant response parsing', () => {
  test('extracts the first assistant message', () => {
    assert.equal(
      parseAssistantReply({
        choices: [{ message: { content: '  Use /v1 as the Base URL.  ' } }],
      }),
      'Use /v1 as the Base URL.'
    )
  })

  test('rejects empty or malformed upstream responses', () => {
    assert.throws(
      () => parseAssistantReply({ error: { message: 'model unavailable' } }),
      /model unavailable/
    )
    assert.throws(
      () => parseAssistantReply({ choices: [] }),
      /Assistant returned no answer/
    )
  })

  test('accepts only known assistant intent headers', () => {
    assert.equal(parseAssistantIntent(' client_setup '), 'client_setup')
    assert.equal(parseAssistantIntent('HUMAN_SUPPORT'), 'human_support')
    assert.equal(parseAssistantIntent('unknown-intent'), undefined)
    assert.equal(parseAssistantIntent(undefined), undefined)
  })

  test('accepts only complete L1 recommendation actions', () => {
    assert.deepEqual(
      parseAssistantAction({
        type: 'l1_recommendation',
        user_statement: '  I am building an internal coding tool. ',
        recommendation: ' Recommend L1 because the use case is concrete. ',
        confirmation_token: ' confirmation-token ',
      }),
      {
        type: 'l1_recommendation',
        user_statement: 'I am building an internal coding tool.',
        recommendation: 'Recommend L1 because the use case is concrete.',
        confirmation_token: 'confirmation-token',
      }
    )
    assert.equal(
      parseAssistantAction({
        type: 'l1_recommendation',
        user_statement: '',
        recommendation: 'missing statement',
      }),
      undefined
    )
    assert.equal(parseAssistantAction({ type: 'open_wallet' }), undefined)
  })

  test('accepts a session-bound API key creation preview without exposing a key', () => {
    assert.deepEqual(
      parseAssistantAction({
        type: 'create_key',
        confirmation_token: ' key-preview-token ',
        requires_confirmation: true,
        expires_in_seconds: 600,
        name: 'test',
        group: 'GPT-Auto',
        key: 'sk-must-never-be-forwarded',
      }),
      {
        type: 'create_key',
        confirmation_token: 'key-preview-token',
        requires_confirmation: true,
        expires_in_seconds: 600,
        name: 'test',
        group: 'GPT-Auto',
      }
    )
  })

  test('accepts the server-issued new-user gift action without private quota data', () => {
    assert.deepEqual(
      parseAssistantAction({
        type: 'new_user_gift',
        amount_cents: 625,
        status: 'offered',
        reason: '  Clear, constructive, and concrete engagement.  ',
        quota: 3_125_000,
      }),
      {
        type: 'new_user_gift',
        amount_cents: 625,
        status: 'offered',
        reason: 'Clear, constructive, and concrete engagement.',
      }
    )
    assert.equal(
      parseAssistantAction({
        type: 'new_user_gift',
        amount_cents: 0,
        status: 'offered',
        reason: 'No gift this time.',
      }),
      undefined
    )
    assert.equal(
      parseAssistantAction({
        type: 'new_user_gift',
        amount_cents: 625,
        status: 'declined',
        reason: 'No gift this time.',
      }),
      undefined
    )
  })

  test('accepts only a bounded weekly discount offer', () => {
    assert.deepEqual(
      parseAssistantAction({
        type: 'weekly_discount',
        discount_percent: 8,
        status: 'offered',
        reason: 'Clear, useful, and constructive work this week.',
      }),
      {
        type: 'weekly_discount',
        discount_percent: 8,
        status: 'offered',
        reason: 'Clear, useful, and constructive work this week.',
      }
    )
    assert.equal(
      parseAssistantAction({
        type: 'weekly_discount',
        discount_percent: 11,
        status: 'offered',
        reason: 'Too large.',
      }),
      undefined
    )
  })

  test('accepts only a complete session-bound human-support confirmation', () => {
    assert.deepEqual(
      parseAssistantAction({
        type: 'human_support',
        confirmation_token: ' handoff-token ',
        requires_confirmation: true,
        expires_in_seconds: 600,
        message: '  Please investigate the failed API request.  ',
      }),
      {
        type: 'human_support',
        confirmation_token: 'handoff-token',
        requires_confirmation: true,
        expires_in_seconds: 600,
        message: 'Please investigate the failed API request.',
      }
    )
    assert.equal(
      parseAssistantAction({
        type: 'human_support',
        confirmation_token: 'handoff-token',
        requires_confirmation: true,
        expires_in_seconds: 600,
        message: 'x',
      })?.type,
      undefined
    )
    assert.equal(
      parseAssistantAction({
        type: 'human_support',
        confirmation_token: 'handoff-token',
        requires_confirmation: false,
        expires_in_seconds: 600,
        message: 'Please investigate the failed API request.',
      }),
      undefined
    )
  })

  test('accepts an image generation confirmation without exposing credentials', () => {
    assert.deepEqual(
      parseAssistantAction({
        type: 'image_generation',
        confirmation_token: ' image-token ',
        requires_confirmation: true,
        expires_in_seconds: 600,
        prompt: '  a quiet workshop at sunrise  ',
        model: 'image-2',
        group: 'image-2',
        n: 2,
        size: '1024x1024',
        quality: 'high',
        api_key: 'must-never-be-forwarded',
      }),
      {
        type: 'image_generation',
        confirmation_token: 'image-token',
        requires_confirmation: true,
        expires_in_seconds: 600,
        prompt: 'a quiet workshop at sunrise',
        model: 'image-2',
        group: 'image-2',
        n: 2,
        size: '1024x1024',
        quality: 'high',
      }
    )
    assert.equal(
      parseAssistantAction({
        type: 'image_generation',
        confirmation_token: 'image-token',
        requires_confirmation: true,
        expires_in_seconds: 600,
        prompt: 'draw',
        model: 'image-2',
        group: 'image-2',
        n: 5,
      }),
      undefined
    )
  })

  test('accepts exact administrator previews and keeps the confirmation token', () => {
    assert.deepEqual(
      parseAssistantAction({
        type: 'admin_config_change',
        confirmation_token: ' admin-token ',
        requires_confirmation: true,
        expires_in_seconds: 600,
        changes: [
          {
            key: 'AssistantModel',
            label: 'Default assistant model ID',
            old_value: 'old-model',
            new_value: 'new-model',
          },
        ],
      }),
      {
        type: 'admin_config_change',
        confirmation_token: 'admin-token',
        requires_confirmation: true,
        expires_in_seconds: 600,
        changes: [
          {
            key: 'AssistantModel',
            label: 'Default assistant model ID',
            old_value: 'old-model',
            new_value: 'new-model',
          },
        ],
      }
    )
    assert.deepEqual(
      parseAssistantAction({
        type: 'admin_config_change',
        confirmation_token: 'scoped-token',
        requires_confirmation: true,
        expires_in_seconds: 600,
        scope: 'channel',
        channel_id: 42,
        channel_name: 'GPT-Pro',
        changes: [
          {
            key: 'channel.model',
            label: 'Model',
            old_value: 'old-model',
            new_value: 'new-model',
          },
        ],
      }),
      {
        type: 'admin_config_change',
        confirmation_token: 'scoped-token',
        requires_confirmation: true,
        expires_in_seconds: 600,
        scope: 'channel',
        channel_id: 42,
        channel_name: 'GPT-Pro',
        changes: [
          {
            key: 'channel.model',
            label: 'Model',
            old_value: 'old-model',
            new_value: 'new-model',
          },
        ],
      }
    )
    assert.equal(
      parseAssistantAction({
        type: 'admin_config_change',
        confirmation_token: 'scoped-token',
        requires_confirmation: true,
        expires_in_seconds: 600,
        scope: 'channel',
        channel_id: 0,
        changes: [
          {
            key: 'channel.model',
            label: 'Model',
            old_value: 'old-model',
            new_value: 'new-model',
          },
        ],
      }),
      undefined
    )
    assert.equal(
      parseAssistantAction({
        type: 'admin_config_change',
        confirmation_token: 'admin-token',
        requires_confirmation: true,
        expires_in_seconds: 600,
        changes: [{ key: 'AssistantModel' }],
      }),
      undefined
    )
    assert.deepEqual(
      parseAssistantAction({
        type: 'admin_pricing_change',
        confirmation_token: ' pricing-token ',
        requires_confirmation: true,
        expires_in_seconds: 600,
        pricing: {
          model_id: 'deepseek-v4-flash',
          old: { mode: 'ratio', value: 1 },
          next: { mode: 'ratio', value: 0.8 },
        },
      }),
      {
        type: 'admin_pricing_change',
        confirmation_token: 'pricing-token',
        requires_confirmation: true,
        expires_in_seconds: 600,
        pricing: {
          model_id: 'deepseek-v4-flash',
          old: { mode: 'ratio', value: 1 },
          next: { mode: 'ratio', value: 0.8 },
        },
      }
    )
    assert.deepEqual(
      parseAssistantAction({
        type: 'admin_model_sync',
        confirmation_token: ' model-sync-token ',
        requires_confirmation: true,
        expires_in_seconds: 600,
        locale: 'zh-CN',
        source_digest: 'a'.repeat(64),
        models: [{ model_id: 'gpt-5.6-sol', vendor: 'OpenAI', status: 1 }],
        vendors: [{ name: 'OpenAI', description: 'model provider', status: 1 }],
      }),
      {
        type: 'admin_model_sync',
        confirmation_token: 'model-sync-token',
        requires_confirmation: true,
        expires_in_seconds: 600,
        locale: 'zh-CN',
        source_digest: 'a'.repeat(64),
        models: [{ model_id: 'gpt-5.6-sol', vendor: 'OpenAI', status: 1 }],
        vendors: [{ name: 'OpenAI', description: 'model provider', status: 1 }],
      }
    )
  })

  test('accepts only allowlisted navigation and confirmation-gated user actions', () => {
    assert.deepEqual(
      parseAssistantAction({
        type: 'navigate',
        path: '/models',
        query: {},
      }),
      {
        type: 'navigate',
        path: '/models',
        query: {},
      }
    )
    assert.deepEqual(
      parseAssistantAction({
        type: 'navigate',
        path: '/users',
        query: { filter: 'alice', l0Only: false },
      }),
      {
        type: 'navigate',
        path: '/users',
        query: { filter: 'alice', l0Only: false },
      }
    )
    assert.equal(
      parseAssistantAction({
        type: 'navigate',
        path: 'https://example.com',
        query: {},
      }),
      undefined
    )
    assert.deepEqual(
      parseAssistantAction({
        type: 'user_password_change',
        requires_confirmation: true,
        target_user_id: 7,
        target_username: 'alice',
        target_display_name: 'Alice',
        target_role: 1,
        target_group: 'default',
        target_is_self: false,
      }),
      {
        type: 'user_password_change',
        requires_confirmation: true,
        target_user_id: 7,
        target_username: 'alice',
        target_display_name: 'Alice',
        target_role: 1,
        target_group: 'default',
        target_is_self: false,
      }
    )
    assert.equal(
      parseAssistantAction({
        type: 'user_password_change',
        requires_confirmation: true,
        target_user_id: 7,
        target_username: 'alice',
        target_display_name: 'Alice',
        target_role: 1,
        target_group: 'default',
        target_is_self: false,
        password: 'should-never-be-accepted',
      }),
      undefined
    )
  })

  test('keeps tool traces bounded and accepts only scalar safe input', () => {
    assert.deepEqual(
      parseAssistantToolTraces([
        {
          name: 'get_user_overview',
          status: 'output-available',
          input: { identifier: 'alice', user_id: 7 },
          output: { email: 'must-not-be-forwarded' },
        },
        {
          name: 'get_user_usage_summary',
          status: 'output-error',
          input: { days: 30 },
        },
        {
          name: 'calculate_math',
          status: 'output-available',
          input: { expression: '6 * 7' },
          result: 42,
        },
        {
          name: 'calculate_math',
          status: 'output-error',
          error_code: 'missing_math_expression',
        },
        {
          name: 'bad',
          status: 'output-available',
          input: { nested: { secret: 'x' } },
          result: 99,
        },
      ]),
      [
        {
          name: 'get_user_overview',
          status: 'output-available',
          input: { identifier: 'alice', user_id: 7 },
        },
        {
          name: 'get_user_usage_summary',
          status: 'output-error',
          input: { days: 30 },
        },
        {
          name: 'calculate_math',
          status: 'output-available',
          input: { expression: '6 * 7' },
          result: 42,
        },
        {
          name: 'calculate_math',
          status: 'output-error',
          errorCode: 'missing_math_expression',
        },
      ]
    )
  })
})

describe('assistant conversation context', () => {
  test('keeps recent multi-turn context and always starts with a user message', () => {
    const history: AssistantChatMessage[] = [
      { role: 'assistant', content: 'orphaned model reply' },
      { role: 'user', content: 'How do I configure Claude Code?' },
      { role: 'assistant', content: 'Choose your operating system.' },
    ]

    assert.deepEqual(
      buildAssistantConversation(history, '  What about Windows?  '),
      [
        { role: 'user', content: 'How do I configure Claude Code?' },
        { role: 'assistant', content: 'Choose your operating system.' },
        { role: 'user', content: 'What about Windows?' },
      ]
    )
  })

  test('bounds context by message count without losing the latest question', () => {
    const history: AssistantChatMessage[] = Array.from(
      { length: 20 },
      (_, index) => ({
        role: index % 2 === 0 ? ('user' as const) : ('assistant' as const),
        content: `message-${index}`,
      })
    )

    const conversation = buildAssistantConversation(history, 'latest')
    assert.ok(conversation.length <= 12)
    assert.equal(conversation[0]?.role, 'user')
    assert.deepEqual(conversation.at(-1), {
      role: 'user',
      content: 'latest',
    })
    assert.equal(
      conversation.some((message) => message.content === 'message-0'),
      false
    )
    assert.equal(
      conversation.some((message) => message.content === 'message-19'),
      true
    )
  })

  test('rejects an oversized latest message before making a request', () => {
    assert.throws(
      () => buildAssistantConversation([], '问'.repeat(4001)),
      /between 1 and 4000 characters/
    )
  })
})

describe('assistant chat retry policy', () => {
  test('consumes assistant SSE deltas before the final response', async () => {
    const originalFetch = globalThis.fetch
    const chunks = [
      'event: ready\ndata: {"type":"ready"}\n\n',
      'event: delta\ndata: {"content":"实时"}\n\n',
      'event: delta\ndata: {"content":"输出"}\n\n',
      'event: done\ndata: {"choices":[{"message":{"content":"实时输出"}}],"lmm_assistant_history":{"conversation_id":9}}\n\n',
    ]
    const deltas: string[] = []
    let requestedAccept = ''
    globalThis.fetch = (async (_input, init) => {
      requestedAccept = new Headers(init?.headers).get('Accept') || ''
      const encoder = new TextEncoder()
      let index = 0
      const body = new ReadableStream<Uint8Array>({
        pull(controller) {
          if (index >= chunks.length) {
            controller.close()
            return
          }
          controller.enqueue(encoder.encode(chunks[index]))
          index += 1
        },
      })
      return new Response(body, {
        headers: {
          'content-type': 'text/event-stream',
          'x-lmm-assistant-intent': 'models',
        },
      })
    }) as typeof globalThis.fetch

    try {
      const reply = await sendAssistantMessage(
        'hello',
        [],
        undefined,
        undefined,
        { onDelta: (content) => deltas.push(content) }
      )
      assert.equal(requestedAccept, 'text/event-stream')
      assert.deepEqual(deltas, ['实时', '输出'])
      assert.deepEqual(reply, {
        content: '实时输出',
        intent: 'models',
        action: undefined,
        conversationId: 9,
      })
    } finally {
      globalThis.fetch = originalFetch
    }
  })

  test('resets the visible message when the redaction boundary replaces content', async () => {
    const originalFetch = globalThis.fetch
    const chunks = [
      'event: delta\ndata: {"content":"safe"}\n\n',
      'event: replace\ndata: {"content":"[REDACTED]"}\n\n',
      'event: done\ndata: {"choices":[{"message":{"content":"[REDACTED]"}}]}\n\n',
    ]
    const deltas: string[] = []
    let resets = 0
    globalThis.fetch = (async () => {
      const encoder = new TextEncoder()
      let index = 0
      return new Response(
        new ReadableStream<Uint8Array>({
          pull(controller) {
            if (index >= chunks.length) {
              controller.close()
              return
            }
            controller.enqueue(encoder.encode(chunks[index]))
            index += 1
          },
        }),
        { headers: { 'content-type': 'text/event-stream' } }
      )
    }) as typeof globalThis.fetch

    try {
      const reply = await sendAssistantMessage(
        'hello',
        [],
        undefined,
        undefined,
        {
          onDelta: (content) => deltas.push(content),
          onReset: () => {
            resets += 1
          },
        }
      )
      assert.equal(resets, 1)
      assert.deepEqual(deltas, ['safe', '[REDACTED]'])
      assert.equal(reply.content, '[REDACTED]')
    } finally {
      globalThis.fetch = originalFetch
    }
  })

  test('immediately clears streamed planning before an agent tool call', async () => {
    const originalFetch = globalThis.fetch
    const chunks = [
      'event: delta\ndata: {"content":"I will check that."}\n\n',
      'event: replace\ndata: {"content":""}\n\n',
      'event: delta\ndata: {"content":"Here are the live models."}\n\n',
      'event: done\ndata: {"choices":[{"message":{"content":"Here are the live models."}}]}\n\n',
    ]
    const deltas: string[] = []
    let resets = 0
    globalThis.fetch = (async () => {
      const encoder = new TextEncoder()
      let index = 0
      return new Response(
        new ReadableStream<Uint8Array>({
          pull(controller) {
            if (index >= chunks.length) {
              controller.close()
              return
            }
            controller.enqueue(encoder.encode(chunks[index]))
            index += 1
          },
        }),
        { headers: { 'content-type': 'text/event-stream' } }
      )
    }) as typeof globalThis.fetch

    try {
      await sendAssistantMessage('hello', [], undefined, undefined, {
        onDelta: (content) => deltas.push(content),
        onReset: () => {
          resets += 1
        },
      })
      assert.equal(resets, 1)
      assert.deepEqual(deltas, [
        'I will check that.',
        'Here are the live models.',
      ])
    } finally {
      globalThis.fetch = originalFetch
    }
  })

  test('does not retry a user-cancelled stream request', async () => {
    const originalFetch = globalThis.fetch
    const controller = new AbortController()
    controller.abort()
    globalThis.fetch = (async () => {
      throw new DOMException('The user cancelled the request.', 'AbortError')
    }) as typeof globalThis.fetch

    try {
      await assert.rejects(
        sendAssistantMessage(
          'hello',
          [],
          undefined,
          undefined,
          { onDelta: () => undefined },
          controller.signal
        ),
        (error: unknown) => isAssistantRequestAborted(error)
      )
    } finally {
      globalThis.fetch = originalFetch
    }
  })

  test('keeps an in-flight stream cancellation out of retry handling', async () => {
    const originalFetch = globalThis.fetch
    globalThis.fetch = (async () => {
      let deliveredDelta = false
      return new Response(
        new ReadableStream<Uint8Array>({
          pull(controller) {
            if (!deliveredDelta) {
              deliveredDelta = true
              controller.enqueue(
                new TextEncoder().encode(
                  'event: delta\ndata: {"content":"partial"}\n\n'
                )
              )
              return
            }
            controller.error(
              new DOMException('The user cancelled the request.', 'AbortError')
            )
          },
        }),
        { headers: { 'content-type': 'text/event-stream' } }
      )
    }) as typeof globalThis.fetch

    try {
      await assert.rejects(
        sendAssistantMessage('hello', [], undefined, undefined, {
          onDelta: () => undefined,
        }),
        (error: unknown) => isAssistantRequestAborted(error)
      )
    } finally {
      globalThis.fetch = originalFetch
    }
  })

  test('marks a safety-terminated conversation from server-owned metadata', async () => {
    const originalPost = api.post
    api.post = (async () => ({
      data: {
        choices: [{ message: { content: 'This conversation has ended.' } }],
        lmm_assistant_policy: 'conversation_restricted',
        lmm_assistant_history: {
          conversation_id: 73,
          restricted: true,
        },
      },
      headers: {},
    })) as typeof api.post

    try {
      assert.deepEqual(await sendAssistantMessage('hello'), {
        content: 'This conversation has ended.',
        intent: undefined,
        action: undefined,
        conversationId: 73,
        restricted: true,
      })
    } finally {
      api.post = originalPost
    }
  })

  test('redacts current and historical secrets at the request boundary', async () => {
    const originalPost = api.post
    let capturedBody: unknown
    api.post = (async (_url: string, data: unknown) => {
      capturedBody = data
      return {
        data: { choices: [{ message: { content: 'safe reply' } }] },
        headers: {},
      }
    }) as typeof api.post

    try {
      await sendAssistantMessage(
        'Explain this error for owner@example.test using sk-current-secret-123456.',
        [
          {
            role: 'user',
            content: 'Earlier token: bearer history-secret-token',
          },
        ]
      )
    } finally {
      api.post = originalPost
    }

    const serializedBody = JSON.stringify(capturedBody)
    assert.doesNotMatch(serializedBody, /owner@example\.test/)
    assert.doesNotMatch(serializedBody, /sk-current-secret-123456/)
    assert.doesNotMatch(serializedBody, /history-secret-token/)
    assert.match(serializedBody, /REDACTED_EMAIL/)
    assert.match(serializedBody, /REDACTED_API_KEY/)
    assert.match(serializedBody, /REDACTED_TOKEN/)
  })

  test('retries transient upstream failures and preserves the attempt header', async () => {
    const originalPost = api.post
    const attempts: string[] = []
    let callCount = 0
    api.post = (async (_url: string, _data: unknown, config: unknown) => {
      callCount += 1
      attempts.push(
        String(
          (config as { headers?: Record<string, string> } | undefined)
            ?.headers?.['X-LMM-Assistant-Attempt']
        )
      )
      if (callCount === 1) throw retryableAxiosError(503)
      return {
        data: { choices: [{ message: { content: 'ready' } }] },
        headers: { 'x-lmm-assistant-intent': 'other' },
      }
    }) as typeof api.post

    try {
      assert.deepEqual(await sendAssistantMessage('hello'), {
        content: 'ready',
        intent: 'other',
        action: undefined,
      })
    } finally {
      api.post = originalPost
    }

    assert.equal(callCount, 2)
    assert.deepEqual(attempts, ['1', '2'])
  })

  test('stops after five total attempts', async () => {
    const originalPost = api.post
    let callCount = 0
    api.post = (async () => {
      callCount += 1
      throw retryableAxiosError(502)
    }) as typeof api.post

    try {
      await assert.rejects(() => sendAssistantMessage('hello'))
    } finally {
      api.post = originalPost
    }

    assert.equal(callCount, ASSISTANT_MAX_REQUEST_ATTEMPTS)
  })
})

describe('assistant conversation history API', () => {
  test('uses the active default and an explicit archived list filter', async () => {
    const originalGet = api.get
    const calls: Array<{ url: string; config: unknown }> = []
    api.get = (async (url: string, config: unknown) => {
      calls.push({ url, config })
      return {
        data: {
          success: true,
          data: { conversations: [] },
        },
      }
    }) as typeof api.get
    try {
      await getAssistantConversationHistory()
      await getAssistantConversationHistory(true)
    } finally {
      api.get = originalGet
    }

    assert.equal(calls[0]?.url, '/api/assistant/conversations')
    assert.equal(
      (calls[0]?.config as { params?: unknown } | undefined)?.params,
      undefined
    )
    assert.deepEqual(
      (calls[1]?.config as { params?: unknown } | undefined)?.params,
      { archived: true }
    )
  })

  test('posts owner archive and restore actions to separate endpoints', async () => {
    const originalPost = api.post
    const calls: Array<{ url: string; data: unknown }> = []
    api.post = (async (url: string, data: unknown) => {
      calls.push({ url, data })
      const archived = !url.endsWith('/unarchive')
      return {
        data: {
          success: true,
          data: { id: 42, archived, archived_at: archived ? 123 : 0 },
        },
      }
    }) as typeof api.post
    try {
      assert.deepEqual(await archiveAssistantConversation(42), {
        id: 42,
        archived: true,
        archived_at: 123,
      })
      assert.deepEqual(await unarchiveAssistantConversation(42), {
        id: 42,
        archived: false,
        archived_at: 0,
      })
    } finally {
      api.post = originalPost
    }

    assert.deepEqual(
      calls.map((call) => call.url),
      [
        '/api/assistant/conversations/42/archive',
        '/api/assistant/conversations/42/unarchive',
      ]
    )
  })
})
