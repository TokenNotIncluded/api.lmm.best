/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
export type CloudMessage = { role: 'user' | 'assistant'; content: string }
export type CloudReply = {
  content: string
  conversationId?: number
  restricted?: boolean
  needsAction?: boolean
}
export type CloudPhase =
  | 'idle'
  | 'waiting'
  | 'streaming'
  | 'done'
  | 'stopped'
  | 'error'
export type CloudTurn = {
  id: string
  question: string
  answer: string
  phase: CloudPhase
}
export type CloudSnapshot = {
  question: string
  answer: string
  phase: CloudPhase
  revision: number
  needsAction: boolean
  turns: readonly CloudTurn[]
}
export type CloudSender = (
  input: {
    message: string
    history: CloudMessage[]
    conversationId?: number
    turnId: string
    presetId?: string
    replay?: boolean
  },
  handlers: { onDelta: (text: string) => void; onReset: () => void },
  signal: AbortSignal
) => Promise<CloudReply>

// UI history and request context have separate limits. Never store either in localStorage.
export const CLOUD_TRANSCRIPT_MAX_TURNS = 24
const CLOUD_TRANSCRIPT_MAX_CHARS = 120_000

/** One request at a time. Animation never gates, delays or fabricates transport. */
export function createL0ChatSession(
  send: CloudSender,
  display: (text: string) => string = (text) => text
) {
  let snapshot: CloudSnapshot = {
    question: '',
    answer: '',
    phase: 'idle',
    revision: 0,
    needsAction: false,
    turns: [],
  }
  let listener: ((state: CloudSnapshot) => void) | undefined
  let controller: AbortController | undefined
  let serial = 0
  let content = ''
  let conversationId: number | undefined
  let history: CloudMessage[] = []
  let timer: ReturnType<typeof setTimeout> | undefined
  let lastInput: Parameters<CloudSender>[0] | undefined
  const busy = () =>
    snapshot.phase === 'waiting' || snapshot.phase === 'streaming'
  const publish = (update: Partial<CloudSnapshot>) => {
    snapshot = { ...snapshot, ...update }
    listener?.(snapshot)
  }
  const flush = () => {
    clearTimeout(timer)
    timer = undefined
    publish({ answer: display(content) })
  }
  const stop = () => {
    serial++
    controller?.abort()
    controller = undefined
    clearTimeout(timer)
    timer = undefined
    if (busy()) publish({ phase: 'stopped', answer: display(content) })
  }
  const rememberVisibleTurn = () => {
    if (!snapshot.question || !lastInput) return snapshot.turns
    const turns = [
      ...snapshot.turns,
      {
        id: lastInput.turnId,
        question: snapshot.question,
        answer: snapshot.answer,
        phase: snapshot.phase,
      },
    ].slice(-CLOUD_TRANSCRIPT_MAX_TURNS)
    let size = turns.reduce(
      (n, turn) => n + turn.question.length + turn.answer.length,
      0
    )
    while (size > CLOUD_TRANSCRIPT_MAX_CHARS && turns.length > 1) {
      const oldest = turns.shift()
      if (!oldest) break
      size -= oldest.question.length + oldest.answer.length
    }
    return turns
  }
  const run = (message: string, presetId?: string, replay = false): boolean => {
    if (busy() || !message.trim()) return false
    const turns = replay ? snapshot.turns : rememberVisibleTurn()
    const request =
      replay && lastInput
        ? { ...lastInput, replay: true }
        : {
            message,
            history: [...history],
            conversationId,
            turnId: crypto.randomUUID(),
            presetId,
          }
    lastInput = request
    content = ''
    const id = ++serial
    controller = new AbortController()
    const signal = controller.signal
    publish({
      question: display(message),
      answer: '',
      phase: 'waiting',
      revision: snapshot.revision + 1,
      needsAction: false,
      turns,
    })
    const current = () => serial === id && !signal.aborted
    const handlers = {
      onDelta: (delta: string) => {
        if (!current() || !delta) return
        content += delta
        if (snapshot.phase === 'waiting') {
          // Deliver the first real text immediately; only subsequent deltas are batched.
          publish({ phase: 'streaming', answer: display(content) })
        } else if (timer === undefined) timer = setTimeout(flush, 24)
      },
      onReset: () => {
        if (!current()) return
        clearTimeout(timer)
        timer = undefined
        content = ''
        publish({
          answer: '',
          phase: 'waiting',
          revision: snapshot.revision + 1,
        })
      },
    }
    void (async () => {
      try {
        const reply = await send(request, handlers, signal)
        if (!current()) return
        clearTimeout(timer)
        timer = undefined
        if (reply.restricted) {
          content = ''
          history = []
          conversationId = undefined
          lastInput = undefined
          publish({
            question: '',
            answer: '',
            turns: [],
            phase: 'error',
            needsAction: true,
          })
          return
        }
        content = reply.content
        if (
          typeof reply.conversationId === 'number' &&
          Number.isSafeInteger(reply.conversationId) &&
          reply.conversationId > 0
        ) {
          conversationId = reply.conversationId
        }
        history.push({ role: 'user', content: display(message) })
        if (content) {
          history.push({ role: 'assistant', content: display(content) })
        }
        history = history.slice(-12)
        publish({
          answer: display(content),
          phase: 'done',
          needsAction: reply.needsAction === true,
        })
      } catch {
        if (!current()) return
        clearTimeout(timer)
        timer = undefined
        publish({ answer: display(content), phase: 'error' })
      } finally {
        if (current()) controller = undefined
      }
    })()
    return true
  }
  return {
    get snapshot() {
      return snapshot
    },
    subscribe(next: (state: CloudSnapshot) => void) {
      listener = next
      listener(snapshot)
      return () => {
        listener = undefined
        stop()
      }
    },
    send: (message: string, presetId?: string) => run(message, presetId),
    retry: () =>
      Boolean(
        lastInput &&
        (snapshot.phase === 'error' || snapshot.phase === 'stopped') &&
        run(lastInput.message, lastInput.presetId, true)
      ),
    stop,
    clear() {
      stop()
      history = []
      conversationId = undefined
      content = ''
      lastInput = undefined
      publish({
        question: '',
        answer: '',
        turns: [],
        phase: 'idle',
        needsAction: false,
        revision: snapshot.revision + 1,
      })
    },
  }
}
