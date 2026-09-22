/*
Copyright (C) 2026 LIghtJUNction

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
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  AssistantStreamError,
  consumeAssistantAISDKStream,
  withAssistantDeadline,
} from './assistant-ai-stream'

function eventStream(text: string, close = true) {
  let cancelled = false
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(new TextEncoder().encode(text))
      if (close) controller.close()
    },
    cancel() {
      cancelled = true
    },
  })
  return { body, wasCancelled: () => cancelled }
}

describe('assistant stream lifecycle', () => {
  test('releases a completed response even when HTTP stays open', async () => {
    const { body, wasCancelled } = eventStream(
      'event: done\ndata: {"choices":[{"message":{"content":"complete"}}]}\n\n',
      false
    )
    assert.deepEqual(await consumeAssistantAISDKStream(body, {}), {
      choices: [{ message: { content: 'complete' } }],
    })
    assert.equal(wasCancelled(), true)
    assert.equal(body.locked, false)
  })

  test('preserves a terminal stream error and releases its connection', async () => {
    const { body, wasCancelled } = eventStream(
      'event: error\ndata: {"status":503,"message":"service disabled","retryable":false}\n\n',
      false
    )
    await assert.rejects(
      consumeAssistantAISDKStream(body, {}),
      (error: unknown) => {
        assert.ok(error instanceof AssistantStreamError)
        assert.equal(error.message, 'service disabled')
        assert.equal(error.retryable, false)
        return true
      }
    )
    assert.equal(wasCancelled(), true)
    assert.equal(body.locked, false)
  })

  test('handles UTF-8 and CRLF boundaries split across individual bytes', async () => {
    const bytes = new TextEncoder().encode(
      ': heartbeat\r\nevent: delta\r\ndata: {"content":"你好🌍"}\r\n\r\n' +
        'event: done\r\ndata: {"choices":[{"message":{"content":"你好🌍"}}]}\r\n\r\n'
    )
    let offset = 0
    const body = new ReadableStream<Uint8Array>({
      pull(controller) {
        if (offset === bytes.length) controller.close()
        else controller.enqueue(bytes.slice(offset, ++offset))
      },
    })
    const deltas: string[] = []
    await consumeAssistantAISDKStream(body, {
      onDelta: (text) => deltas.push(text),
    })
    assert.deepEqual(deltas, ['你好🌍'])
    assert.equal(body.locked, false)
  })

  test('rejects a disconnected partial answer instead of treating it as complete', async () => {
    const { body } = eventStream(
      'event: delta\ndata: {"content":"partial"}\n\n'
    )
    const deltas: string[] = []
    await assert.rejects(
      consumeAssistantAISDKStream(body, {
        onDelta: (text) => deltas.push(text),
      }),
      /ended before completion/
    )
    assert.deepEqual(deltas, ['partial'])
  })

  test('rejects non-object event data with a protocol error', async () => {
    for (const value of ['null', '[]', '"unexpected"']) {
      const { body } = eventStream(`event: done\ndata: ${value}\n\n`)
      await assert.rejects(
        consumeAssistantAISDKStream(body, {}),
        /invalid event data/
      )
    }
  })
})

describe('assistant bounded execution', () => {
  test('times out an idle response and releases its reader', async () => {
    const { body, wasCancelled } = eventStream('', false)
    await assert.rejects(
      consumeAssistantAISDKStream(
        body,
        {},
        { idleTimeoutMs: 10, timeoutMs: 500 }
      ),
      (error: unknown) =>
        error instanceof AssistantStreamError &&
        error.status === 408 &&
        !error.retryable
    )
    assert.equal(wasCancelled(), true)
    assert.equal(body.locked, false)
  })

  test('heartbeats cannot renew the total request budget', async () => {
    let interval: ReturnType<typeof setInterval> | undefined
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        interval = setInterval(
          () => controller.enqueue(new TextEncoder().encode(': keepalive\n\n')),
          2
        )
      },
      cancel() {
        clearInterval(interval)
      },
    })
    try {
      await assert.rejects(
        consumeAssistantAISDKStream(
          body,
          {},
          { idleTimeoutMs: 100, timeoutMs: 25 }
        ),
        (error: unknown) =>
          error instanceof AssistantStreamError && !error.retryable
      )
      // The deadline cancels pending reads, not only the outer fetch.
      await new Promise((resolve) => setTimeout(resolve, 0))
      assert.equal(body.locked, false)
    } finally {
      clearInterval(interval)
    }
  })

  test('cancels a pending read even when the source ignores fetch cancellation', async () => {
    const { body, wasCancelled } = eventStream('', false)
    const controller = new AbortController()
    const result = consumeAssistantAISDKStream(
      body,
      {},
      { signal: controller.signal }
    )
    controller.abort()
    await assert.rejects(
      result,
      (error: unknown) => error instanceof Error && error.name === 'AbortError'
    )
    await new Promise((resolve) => setTimeout(resolve, 0))
    assert.equal(wasCancelled(), true)
    assert.equal(body.locked, false)
  })

  test('never awaits a stuck underlying cancel hook', async () => {
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(
          new TextEncoder().encode(
            'event: done\ndata: {"choices":[{"message":{"content":"finished"}}]}\n\n'
          )
        )
      },
      cancel() {
        return new Promise<void>(() => undefined)
      },
    })
    await consumeAssistantAISDKStream(body, {}, { timeoutMs: 50 })
    assert.equal(body.locked, false)
  })

  test('rejects unterminated oversized events without retrying', async () => {
    const { body } = eventStream(`data: ${'x'.repeat(512 * 1024)}`, false)
    await assert.rejects(
      consumeAssistantAISDKStream(body, {}),
      (error: unknown) =>
        error instanceof AssistantStreamError &&
        !error.retryable &&
        /size limit/.test(error.message)
    )
    assert.equal(body.locked, false)
  })

  test('does not replay a run after tool execution starts', async () => {
    const { body } = eventStream(
      'event: progress\ndata: {"phase":"tool","step":2,"arguments":"must not be exposed"}\n\n' +
        'event: error\ndata: {"status":503,"message":"provider failed","retryable":true}\n\n'
    )
    const progress: unknown[] = []
    await assert.rejects(
      consumeAssistantAISDKStream(body, {
        onProgress: (value) => progress.push(value),
      }),
      (error: unknown) =>
        error instanceof AssistantStreamError && !error.retryable
    )
    assert.deepEqual(progress, [{ phase: 'tool', step: 2 }])
  })

  test('a truncated response remains non-retryable after partial text', async () => {
    const { body } = eventStream(
      'event: delta\ndata: {"content":"partial"}\n\n'
    )
    await assert.rejects(
      consumeAssistantAISDKStream(body, {}),
      (error: unknown) =>
        error instanceof AssistantStreamError && !error.retryable
    )
  })

  test('ignores events after terminal completion in the same chunk', async () => {
    const { body } = eventStream(
      'event: done\ndata: {}\n\nevent: delta\ndata: {"content":"late"}\n\n'
    )
    const deltas: string[] = []
    await consumeAssistantAISDKStream(body, {
      onDelta: (delta) => deltas.push(delta),
    })
    assert.deepEqual(deltas, [])
  })
})

describe('assistant request-wide deadline', () => {
  test('settles even while authentication never resolves', async () => {
    let signal: AbortSignal | undefined
    await assert.rejects(
      withAssistantDeadline(
        async (current) => {
          signal = current
          return new Promise<never>(() => undefined)
        },
        undefined,
        10
      ),
      (error: unknown) =>
        error instanceof AssistantStreamError && !error.retryable
    )
    assert.equal(signal?.aborted, true)
  })

  test('already-cancelled requests never start work', async () => {
    const controller = new AbortController()
    controller.abort()
    let calls = 0
    await assert.rejects(
      withAssistantDeadline(async () => {
        calls++
        return null
      }, controller.signal),
      (error: unknown) => error instanceof Error && error.name === 'AbortError'
    )
    assert.equal(calls, 0)
  })
})

describe('SSE line endings', () => {
  test('finishes a CR-delimited response without waiting for HTTP EOF', async () => {
    const { body, wasCancelled } = eventStream(
      'event: delta\rdata: {"content":"ready"}\r\revent: done\rdata: {"content":"ready"}\r\r',
      false
    )
    const deltas: string[] = []
    const result = await consumeAssistantAISDKStream(
      body,
      { onDelta: (text) => deltas.push(text) },
      { timeoutMs: 200 }
    )
    assert.deepEqual(deltas, ['ready'])
    assert.deepEqual(result, { content: 'ready' })
    assert.equal(wasCancelled(), true)
  })

  test('mixed endings and byte-split UTF-8 dispatch each delta once', async () => {
    const bytes = new TextEncoder().encode(
      ': heartbeat\r\nevent: delta\rdata: {"content":"你好👨‍👩‍👧"}\r\n\r\n' +
        'event: delta\ndata: {"content":" again"}\n\n' +
        'event: done\rdata: {"content":"complete"}\r\r'
    )
    let offset = 0
    const body = new ReadableStream<Uint8Array>({
      pull(controller) {
        if (offset === bytes.length) controller.close()
        else controller.enqueue(bytes.slice(offset, ++offset))
      },
    })
    const deltas: string[] = []
    await consumeAssistantAISDKStream(body, {
      onDelta: (text) => deltas.push(text),
    })
    assert.deepEqual(deltas, ['你好👨‍👩‍👧', ' again'])
    assert.equal(body.locked, false)
  })

  test('CR framing still rejects a truncated response', async () => {
    const { body } = eventStream(
      'event: delta\rdata: {"content":"partial"}\r\r'
    )
    await assert.rejects(
      consumeAssistantAISDKStream(body, {}),
      /ended before completion/
    )
  })
})
