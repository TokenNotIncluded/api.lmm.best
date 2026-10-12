/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { useEffect, useState } from 'react'

export const REMOTE_STATE_TTL_MS = 45_000
/** A successful poll must not keep an old heartbeat live indefinitely. */
export function isRemoteStateFresh(timestamp: unknown, now: number): boolean {
  return (
    typeof timestamp === 'number' &&
    Number.isSafeInteger(timestamp) &&
    timestamp > 0 &&
    timestamp <= now + 5_000 &&
    now - timestamp < REMOTE_STATE_TTL_MS
  )
}

export function useRemoteClock(): number {
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    const tick = () => setNow(Date.now())
    const timer = setInterval(tick, 1_000)
    // A suspended/background tab must recheck expiry before interaction resumes.
    window.addEventListener('focus', tick)
    document.addEventListener('visibilitychange', tick)
    return () => {
      clearInterval(timer)
      window.removeEventListener('focus', tick)
      document.removeEventListener('visibilitychange', tick)
    }
  }, [])
  return now
}
