/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from '@/lib/api'
import { createRemoteCommand, type RemoteCommandInput } from './commands'
import { encryptPiRemoteCommand } from './crypto'
import type { PiMessagesResponse, RemoteControlMessage } from './types'

type Pending = { resolve: () => void; reject: (error: Error) => void; timer: ReturnType<typeof setTimeout>; abort: AbortController }

export function useRemoteCommands(sessionId: string, key: CryptoKey, messages: RemoteControlMessage[]) {
  const pending = useRef(new Map<string, Pending>())
  const mounted = useRef(true)
  const [count, setCount] = useState(0)
  useEffect(() => {
    mounted.current = true
    const requests = pending.current
    return () => {
      mounted.current = false
      for (const request of requests.values()) {
        clearTimeout(request.timer)
        request.abort.abort()
        request.reject(new Error('Session locked'))
      }
      requests.clear()
    }
  }, [sessionId, key])
  useEffect(() => {
    for (const message of messages) {
      if (message.type !== 'ack' || !message.command_id) continue
      const request = pending.current.get(message.command_id)
      if (!request) continue
      pending.current.delete(message.command_id)
      clearTimeout(request.timer)
      if (message.ok) request.resolve()
      else request.reject(new Error(message.content || 'Pi rejected the command'))
      setCount(pending.current.size)
    }
  }, [messages])
  const send = useCallback(async (input: RemoteCommandInput) => {
    if (!mounted.current || pending.current.size >= 4) throw new Error('Too many pending commands')
    const command = createRemoteCommand(input)
    const abort = new AbortController()
    // Register before POST so an acknowledgment cannot race the HTTP response.
    return new Promise<void>((resolve, reject) => {
      const fail = (error: Error) => {
        const request = pending.current.get(command.id)
        if (!request) return
        pending.current.delete(command.id)
        clearTimeout(request.timer)
        abort.abort()
        if (mounted.current) setCount(pending.current.size)
        reject(error)
      }
      const timer = setTimeout(() => fail(new Error('Not confirmed by Pi. Check the session before retrying.')), 20_000)
      pending.current.set(command.id, { resolve, reject, timer, abort })
      setCount(pending.current.size)
      void (async () => {
        try {
          const encrypted = await encryptPiRemoteCommand(sessionId, command, key)
          abort.signal.throwIfAborted()
          const response = await api.post<PiMessagesResponse>(
            `/api/remote-control/v1/pi/sessions/${encodeURIComponent(sessionId)}/messages`,
            { sender: 'controller', ...encrypted },
            { skipBusinessError: true, signal: abort.signal }
          )
          if (!response.data.success) throw new Error('The relay rejected the command')
        } catch { fail(new Error('Command delivery was not confirmed. Check Pi before retrying.')) }
      })()
    })
  }, [sessionId, key])
  return { send, pending: count > 0 }
}
