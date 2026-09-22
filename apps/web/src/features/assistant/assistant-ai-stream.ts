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
// The server owns the SSE envelope. Consume it directly: the old second
// UIMessage stream had an independent lifetime but no independent consumer UI.
export function isRetryableAssistantStatus(status: number): boolean {
  return (
    status === 408 ||
    status === 425 ||
    status === 429 ||
    (status >= 500 && status <= 599)
  )
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

// One ceiling covers auth refresh, HTTP, all model/tool rounds and backoff.
// It must exceed the server's maximum configured 300-second run, not multiply
// that timeout by the number of browser retries.
export const ASSISTANT_REQUEST_TIMEOUT_MS = 310_000
const ASSISTANT_STREAM_IDLE_TIMEOUT_MS = 45_000
const ASSISTANT_STREAM_EVENT_MAX_CHARS = 512 * 1024

export type AssistantProgress = {
  phase: 'model' | 'tool' | 'answer'
  step: number
}

export function assistantTimeoutError(
  code = 'ASSISTANT_REQUEST_TIMEOUT'
): AssistantStreamError {
  const message =
    'The assistant request timed out. Check completed actions before retrying.'
  return new AssistantStreamError(
    408,
    { code, message, retryable: false },
    message,
    false
  )
}

export function assistantAbortReason(signal: AbortSignal): Error {
  return signal.reason instanceof Error
    ? signal.reason
    : new DOMException('The assistant request was cancelled.', 'AbortError')
}

// Racing the caller as well as aborting fetch matters while auth refresh or a
// non-conforming stream source is pending. The caller must always settle.
export async function withAssistantDeadline<T>(
  run: (signal: AbortSignal) => Promise<T>,
  parent?: AbortSignal,
  timeoutMs = ASSISTANT_REQUEST_TIMEOUT_MS
): Promise<T> {
  if (parent?.aborted) throw assistantAbortReason(parent)
  const controller = new AbortController()
  const abort = () =>
    controller.abort(parent ? assistantAbortReason(parent) : undefined)
  parent?.addEventListener('abort', abort, { once: true })
  const timer = setTimeout(
    () => controller.abort(assistantTimeoutError()),
    timeoutMs
  )
  let rejectAbort: () => void = () => undefined
  const cancelled = new Promise<never>((_resolve, reject) => {
    rejectAbort = () => reject(assistantAbortReason(controller.signal))
    controller.signal.addEventListener('abort', rejectAbort, { once: true })
  })
  try {
    return await Promise.race([run(controller.signal), cancelled])
  } finally {
    clearTimeout(timer)
    parent?.removeEventListener('abort', abort)
    controller.signal.removeEventListener('abort', rejectAbort)
    // Stop any response body still draining after a terminal error.
    controller.abort()
  }
}

type AssistantStreamPayload = Record<string, unknown>
type StreamHandlers = {
  onDelta?: (content: string) => void
  onReset?: () => void
  onProgress?: (progress: AssistantProgress) => void
}
type StreamOptions = {
  signal?: AbortSignal
  timeoutMs?: number
  idleTimeoutMs?: number
}

// Keep the export stable for existing consumers. There is one reader, one
// terminal result and one cleanup path; HTTP EOF is not a successful answer.
export async function consumeAssistantAISDKStream(
  body: ReadableStream<Uint8Array>,
  handlers: StreamHandlers,
  options: StreamOptions = {}
): Promise<AssistantStreamPayload> {
  return withAssistantDeadline(
    async (signal) => {
      const reader = body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''
      let skipLeadingLF = false
      // SSE allows CR, LF and CRLF. Preserve a CRLF boundary split across reads,
      // including decoder calls that produce no characters for partial UTF-8.
      const normalizeLines = (text: string) => {
        if (!text) return text
        const start = skipLeadingLF && text.startsWith('\n') ? 1 : 0
        skipLeadingLF = text.endsWith('\r')
        return text.slice(start).replaceAll(/\r\n?/g, '\n')
      }
      let eventName = ''
      let eventData: string[] = []
      let eventSize = 0
      let result: AssistantStreamPayload | undefined
      let activitySeen = false
      let idleTimer: ReturnType<typeof setTimeout> | undefined
      let rejectStopped: (error: Error) => void = () => undefined
      const stopped = new Promise<never>((_resolve, reject) => {
        rejectStopped = reject
      })
      const abort = () => rejectStopped(assistantAbortReason(signal))
      signal.addEventListener('abort', abort, { once: true })
      const resetIdle = () => {
        clearTimeout(idleTimer)
        idleTimer = setTimeout(
          () =>
            rejectStopped(
              assistantTimeoutError('ASSISTANT_STREAM_IDLE_TIMEOUT')
            ),
          options.idleTimeoutMs ?? ASSISTANT_STREAM_IDLE_TIMEOUT_MS
        )
      }
      const protocolError = (message: string) =>
        new AssistantStreamError(
          502,
          { message, retryable: false },
          message,
          false
        )
      const dispatch = () => {
        const data = eventData.join('\n').trim()
        const event = eventName || 'message'
        eventName = ''
        eventData = []
        eventSize = 0
        if (!data || data === '[DONE]' || result) return
        let payload: AssistantStreamPayload
        try {
          payload = JSON.parse(data) as AssistantStreamPayload
          if (
            !payload ||
            typeof payload !== 'object' ||
            Array.isArray(payload)
          ) {
            throw new Error()
          }
        } catch {
          throw protocolError('Assistant stream returned invalid event data')
        }
        if (event === 'error') {
          const status =
            typeof payload.status === 'number' ? payload.status : 502
          const message =
            typeof payload.message === 'string'
              ? payload.message
              : 'AI assistant stream failed'
          throw new AssistantStreamError(
            status,
            payload,
            message,
            !activitySeen &&
              (typeof payload.retryable === 'boolean'
                ? payload.retryable
                : isRetryableAssistantStatus(status))
          )
        }
        if (event === 'done') {
          result = payload
          return
        }
        if (event === 'progress') {
          if (
            (payload.phase === 'model' ||
              payload.phase === 'tool' ||
              payload.phase === 'answer') &&
            typeof payload.step === 'number' &&
            Number.isInteger(payload.step) &&
            payload.step > 0 &&
            payload.step <= 32
          ) {
            if (payload.phase === 'tool') activitySeen = true
            handlers.onProgress?.({ phase: payload.phase, step: payload.step })
          }
          return
        }
        if (
          (event === 'delta' || event === 'replace') &&
          typeof payload.content === 'string'
        ) {
          activitySeen ||= payload.content.length > 0
          if (event === 'replace') handlers.onReset?.()
          if (payload.content) handlers.onDelta?.(payload.content)
        }
      }
      const processLine = (rawLine: string) => {
        const line = rawLine.endsWith('\r') ? rawLine.slice(0, -1) : rawLine
        eventSize += line.length
        if (eventSize > ASSISTANT_STREAM_EVENT_MAX_CHARS) {
          throw protocolError('Assistant stream event exceeds its size limit')
        }
        if (!line) dispatch()
        else if (line.startsWith('event:')) eventName = line.slice(6).trim()
        else if (line.startsWith('data:')) {
          eventData.push(line.slice(5).replace(/^ /, ''))
        }
      }
      try {
        if (signal.aborted) throw assistantAbortReason(signal)
        resetIdle()
        while (!result) {
          const { done, value } = await Promise.race([reader.read(), stopped])
          if (signal.aborted) throw assistantAbortReason(signal)
          if (done) {
            buffer += normalizeLines(decoder.decode())
            if (buffer) processLine(buffer)
            dispatch()
            if (!result) {
              throw protocolError('Assistant stream ended before completion')
            }
            break
          }
          // Heartbeats reset idle time, never the absolute request deadline.
          if (value.byteLength) resetIdle()
          buffer += normalizeLines(decoder.decode(value, { stream: true }))
          let index: number
          while ((index = buffer.indexOf('\n')) >= 0) {
            const line = buffer.slice(0, index)
            buffer = buffer.slice(index + 1)
            processLine(line)
            if (result) break
          }
          if (
            !result &&
            buffer.length + eventSize > ASSISTANT_STREAM_EVENT_MAX_CHARS
          ) {
            throw protocolError('Assistant stream event exceeds its size limit')
          }
        }
        if (!result) {
          throw protocolError('Assistant stream ended before completion')
        }
        return result
      } catch (error) {
        if (signal.aborted) throw assistantAbortReason(signal)
        if (
          error instanceof AssistantStreamError ||
          (error instanceof Error && error.name === 'AbortError')
        ) {
          throw error
        }
        throw protocolError('Assistant stream could not be read')
      } finally {
        clearTimeout(idleTimer)
        signal.removeEventListener('abort', abort)
        // A custom cancel hook can itself hang. Cancel the reader synchronously
        // but never make cleanup wait for the underlying source's promise.
        void reader.cancel().catch(() => undefined)
        reader.releaseLock()
      }
    },
    options.signal,
    options.timeoutMs
  )
}
