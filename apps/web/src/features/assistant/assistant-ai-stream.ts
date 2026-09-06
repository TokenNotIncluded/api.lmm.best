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
import { readUIMessageStream, type UIMessageChunk } from 'ai'

export function isRetryableAssistantStatus(status: number): boolean {
  return status === 408 || status === 425 || status === 429 || status >= 500
}

export class AssistantStreamError extends Error {
  readonly response: { status: number; data: unknown }
  readonly status: number
  readonly retryable: boolean

  constructor(
    status: number,
    data: unknown,
    message: string,
    retryable = isRetryableAssistantStatus(status)
  ) {
    super(message)
    this.name = 'AssistantStreamError'
    this.status = status
    this.response = { status, data }
    this.retryable = retryable
  }
}

type AssistantStreamPayload = Record<string, unknown>

type AssistantAISDKStream = {
  messageStream: ReadableStream<UIMessageChunk>
  completion: Promise<AssistantStreamPayload>
}

type AssistantTextEvent =
  | { type: 'delta'; content: string }
  | { type: 'replace'; content: string }

// The Go API intentionally owns an SSE envelope so it can redact content and
// attach product-only confirmation metadata. This adapter turns that envelope
// into AI SDK UIMessage chunks, keeping the UI on the SDK's streaming state
// model without exposing provider events to the browser.
function createAssistantAISDKStream(
  body: ReadableStream<Uint8Array>,
  onTextEvent?: (event: AssistantTextEvent) => void
): AssistantAISDKStream {
  let resolveCompletion: (payload: AssistantStreamPayload) => void
  let rejectCompletion: (reason?: unknown) => void
  const completion = new Promise<AssistantStreamPayload>((resolve, reject) => {
    resolveCompletion = resolve
    rejectCompletion = reject
  })

  const messageStream = new ReadableStream<UIMessageChunk>({
    start(controller) {
      let buffer = ''
      let eventName = ''
      let eventData: string[] = []
      let textPartIndex = 0
      let textPartOpen = false
      let settled = false

      const textPartID = () => `assistant-text-${textPartIndex}`
      const beginTextPart = () => {
        if (textPartOpen) return
        controller.enqueue({ type: 'text-start', id: textPartID() })
        textPartOpen = true
      }
      const endTextPart = () => {
        if (!textPartOpen) return
        controller.enqueue({ type: 'text-end', id: textPartID() })
        textPartOpen = false
      }
      const fail = (error: Error) => {
        if (settled) return
        settled = true
        rejectCompletion(error)
        controller.error(error)
      }
      const finish = (payload: AssistantStreamPayload) => {
        if (settled) return
        settled = true
        endTextPart()
        controller.enqueue({ type: 'finish', finishReason: 'stop' })
        resolveCompletion(payload)
        controller.close()
      }

      const dispatch = () => {
        const data = eventData.join('\n').trim()
        eventData = []
        const currentEvent = eventName || 'message'
        eventName = ''
        if (!data || data === '[DONE]' || settled) return

        let payload: AssistantStreamPayload
        try {
          payload = JSON.parse(data) as AssistantStreamPayload
          if (
            !payload ||
            typeof payload !== 'object' ||
            Array.isArray(payload)
          ) {
            throw new Error('Expected an assistant event object')
          }
        } catch {
          fail(
            new AssistantStreamError(
              502,
              { message: 'Assistant stream returned invalid event data' },
              'Assistant stream returned invalid event data'
            )
          )
          return
        }

        if (currentEvent === 'delta') {
          if (typeof payload.content === 'string') {
            onTextEvent?.({ type: 'delta', content: payload.content })
            beginTextPart()
            controller.enqueue({
              type: 'text-delta',
              id: textPartID(),
              delta: payload.content,
            })
          }
          return
        }
        if (currentEvent === 'replace') {
          endTextPart()
          textPartIndex += 1
          if (typeof payload.content === 'string') {
            onTextEvent?.({ type: 'replace', content: payload.content })
            if (payload.content !== '') {
              beginTextPart()
              controller.enqueue({
                type: 'text-delta',
                id: textPartID(),
                delta: payload.content,
              })
            }
          }
          return
        }
        if (currentEvent === 'error') {
          const status =
            typeof payload.status === 'number' ? payload.status : 502
          const message =
            typeof payload.message === 'string'
              ? payload.message
              : 'AI assistant stream failed'
          fail(
            new AssistantStreamError(
              status,
              payload,
              message,
              typeof payload.retryable === 'boolean'
                ? payload.retryable
                : undefined
            )
          )
          return
        }
        if (currentEvent === 'done') finish(payload)
      }

      const processLine = (rawLine: string) => {
        const line = rawLine.endsWith('\r') ? rawLine.slice(0, -1) : rawLine
        if (line === '') {
          dispatch()
        } else if (line.startsWith('event:')) {
          eventName = line.slice(6).trim()
        } else if (line.startsWith('data:')) {
          let data = line.slice(5)
          if (data.startsWith(' ')) data = data.slice(1)
          eventData.push(data)
        }
      }

      const consume = async () => {
        let reader: ReadableStreamDefaultReader<Uint8Array> | undefined
        try {
          reader = body.getReader()
          const decoder = new TextDecoder()
          controller.enqueue({ type: 'start', messageId: 'assistant-message' })
          while (!settled) {
            const { done, value } = await reader.read()
            if (done) break
            buffer += decoder.decode(value, { stream: true })
            const lines = buffer.split('\n')
            buffer = lines.pop() || ''
            lines.forEach(processLine)
          }
          buffer += decoder.decode()
          if (buffer) buffer.split('\n').forEach(processLine)
          dispatch()
          if (!settled) {
            fail(
              new AssistantStreamError(
                502,
                { message: 'Assistant stream ended before completion' },
                'Assistant stream ended before completion'
              )
            )
          }
        } catch (error) {
          if (error instanceof AssistantStreamError) {
            fail(error)
            return
          }
          if (error instanceof Error && error.name === 'AbortError') {
            fail(error)
            return
          }
          fail(
            new AssistantStreamError(
              502,
              { message: 'Assistant stream could not be read' },
              error instanceof Error
                ? error.message
                : 'Assistant stream could not be read'
            )
          )
        } finally {
          // A done/error event can arrive before HTTP closes the connection.
          // Stop reading that response before retrying or starting another turn.
          if (reader) {
            await reader.cancel().catch(() => undefined)
            reader.releaseLock()
          }
        }
      }

      void consume()
    },
  })

  return { messageStream, completion }
}

export async function consumeAssistantAISDKStream(
  body: ReadableStream<Uint8Array>,
  handlers: {
    onDelta?: (content: string) => void
    onReset?: () => void
  }
): Promise<AssistantStreamPayload> {
  const { messageStream, completion } = createAssistantAISDKStream(
    body,
    (event) => {
      if (event.type === 'replace') {
        handlers.onReset?.()
        if (event.content) handlers.onDelta?.(event.content)
        return
      }
      handlers.onDelta?.(event.content)
    }
  )
  const drainMessages = async () => {
    for await (const _message of readUIMessageStream({
      stream: messageStream,
    })) {
      // Draining the AI SDK stream preserves its lifecycle validation while the
      // raw event callback above applies replacement semantics losslessly.
    }
  }
  const [payload] = await Promise.all([completion, drainMessages()])
  return payload
}
