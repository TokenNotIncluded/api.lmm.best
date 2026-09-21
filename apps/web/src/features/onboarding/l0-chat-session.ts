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
export type CloudSnapshot = {
  question: string
  answer: string
  phase: 'idle' | 'waiting' | 'streaming' | 'done' | 'stopped' | 'error'
  revision: number
  needsAction: boolean
}
export type CloudSender = (
  input: {
    message: string
    history: CloudMessage[]
    conversationId?: number
    turnId: string
  },
  handlers: { onDelta: (text: string) => void; onReset: () => void },
  signal: AbortSignal
) => Promise<CloudReply>

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
  }
  let listener: ((state: CloudSnapshot) => void) | undefined
  let controller: AbortController | undefined
  let serial = 0
  let content = ''
  let conversationId: number | undefined
  let history: CloudMessage[] = []
  let timer: ReturnType<typeof setTimeout> | undefined
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
    send(message: string): boolean {
      if (busy() || !message.trim()) return false
      content = ''
      const id = ++serial
      controller = new AbortController()
      const signal = controller.signal
      publish({
        question: message,
        answer: '',
        phase: 'waiting',
        revision: snapshot.revision + 1,
        needsAction: false,
      })
      const current = () => serial === id && !signal.aborted
      const handlers = {
        onDelta: (delta: string) => {
          if (!current()) return
          content += delta
          if (snapshot.phase === 'waiting') publish({ phase: 'streaming' })
          if (timer === undefined) timer = setTimeout(flush, 24)
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
          const reply = await send(
            {
              message,
              history: [...history],
              conversationId,
              turnId: crypto.randomUUID(),
            },
            handlers,
            signal
          )
          if (!current()) return
          clearTimeout(timer)
          timer = undefined
          if (reply.restricted) {
            content = ''
            history = []
            conversationId = undefined
            publish({ answer: '', phase: 'error', needsAction: true })
            return
          }
          content = reply.content
          if (
            Number.isSafeInteger(reply.conversationId) &&
            reply.conversationId! > 0
          ) {
            conversationId = reply.conversationId
          }
          history.push({ role: 'user', content: message })
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
    },
    stop,
    clear() {
      stop()
      history = []
      conversationId = undefined
      content = ''
      publish({
        question: '',
        answer: '',
        phase: 'idle',
        needsAction: false,
        revision: snapshot.revision + 1,
      })
    },
  }
}
