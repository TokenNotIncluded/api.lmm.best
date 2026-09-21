/*
Copyright (C) 2026 LIghtJUNction
*/
import { useEffect, useMemo, useSyncExternalStore } from 'react'

import { useAuthStore } from '@/stores/auth-store'

import { DrawingHistory } from './drawing-history'
import { clearActiveDrawingTask } from './drawing-task-state'

// A request keeps its history alive across route unmounts until its result is
// persisted. The last view/request releases the session and its object URLs.
const sessions = new Map<number, ReturnType<typeof createSession>>()

function createSession(userId: number) {
  const history = new DrawingHistory(userId)
  let owners = 0
  let unsubscribe: (() => void) | undefined
  const retain = () => {
    if (owners++ === 0) {
      sessions.set(userId, session)
      history.start()
      unsubscribe = useAuthStore.subscribe((state) => {
        if (state.auth.user?.id !== userId) {
          history.dispose()
          clearActiveDrawingTask(userId)
          if (sessions.get(userId) === session) sessions.delete(userId)
        }
      })
    }
    let released = false
    return () => {
      if (released) return
      released = true
      if (--owners === 0) {
        history.dispose()
        unsubscribe?.()
        if (sessions.get(userId) === session) sessions.delete(userId)
      }
    }
  }
  const session = { history, retain }
  return session
}

export function useDrawingHistory(userId: number) {
  const session = useMemo(
    () => sessions.get(userId) ?? createSession(userId),
    [userId]
  )
  const { history } = session
  useEffect(() => session.retain(), [session])
  const state = useSyncExternalStore(
    history.subscribe,
    history.getSnapshot,
    history.getSnapshot
  )
  return {
    ...state,
    capture: history.capture,
    remember: history.remember,
    clear: history.clear,
    retain: session.retain,
  }
}
