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
