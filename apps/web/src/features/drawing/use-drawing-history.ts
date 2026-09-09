/*
Copyright (C) 2026 LIghtJUNction
*/
import { useEffect, useMemo, useSyncExternalStore } from 'react'

import { DrawingHistory } from './drawing-history'

export function useDrawingHistory(userId: number) {
  const history = useMemo(() => new DrawingHistory(userId), [userId])
  useEffect(() => {
    history.start()
    return history.dispose
  }, [history])
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
  }
}
