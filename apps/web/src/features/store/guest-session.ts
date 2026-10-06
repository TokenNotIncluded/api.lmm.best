/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useCallback, useEffect, useRef, useState } from 'react'

import { storeGuestApi, type StoreGuestSession } from './access-api'
import { STORE_ACCESS_COPY as copy } from './access-copy'
import type { StoreDisclaimer } from './types'

const SESSION_KEY = 'lmm.store.guest-session.v1'
let creating: Promise<StoreGuestSession> | null = null
export function readStoreGuestSession(): StoreGuestSession | null {
  try {
    const raw: unknown = JSON.parse(
      sessionStorage.getItem(SESSION_KEY) || 'null'
    )
    if (!raw || typeof raw !== 'object') return null
    const session = raw as Partial<StoreGuestSession>
    if (
      typeof session.token !== 'string' ||
      !/^[A-Za-z0-9_-]{43}$/.test(session.token) ||
      typeof session.expires_at !== 'number' ||
      !Number.isSafeInteger(session.expires_at) ||
      typeof session.guest_id !== 'string' ||
      !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(
        session.guest_id
      )
    ) {
      return null
    }
    return {
      token: session.token,
      expires_at: session.expires_at,
      guest_id: session.guest_id,
    }
  } catch {
    return null
  }
}
async function getSession() {
  const existing = readStoreGuestSession()
  // Keep an existing actor, even after a deadline. A pending order must never
  // silently become a new anonymous buyer when a local timer expires.
  if (existing) return existing
  if (!creating) {
    creating = storeGuestApi
      .session()
      .then((session) => {
        if (
          typeof session?.token !== 'string' ||
          !/^[A-Za-z0-9_-]{43}$/.test(session.token) ||
          typeof session.expires_at !== 'number' ||
          !Number.isSafeInteger(session.expires_at) ||
          !session.guest_id ||
          typeof session.guest_id !== 'string' ||
          !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(
            session.guest_id
          )
        ) {
          throw new Error(copy.guestSessionFailed)
        }
        sessionStorage.setItem(SESSION_KEY, JSON.stringify(session))
        return session
      })
      .finally(() => {
        creating = null
      })
  }
  return creating
}
export function useStoreGuestSession(enabled: boolean) {
  const [session, setSession] = useState<StoreGuestSession | null>(null)
  const [actorScope, setActorScope] = useState('')
  const [loading, setLoading] = useState(enabled)
  const [error, setError] = useState<unknown>(null)
  const start = useCallback(async () => {
    if (!enabled) return
    setLoading(true)
    setError(null)
    try {
      const result = await getSession()
      const scope = `guest:${result.guest_id}`
      setSession(result)
      setActorScope(scope)
    } catch (issue) {
      setError(issue)
    } finally {
      setLoading(false)
    }
  }, [enabled])
  useEffect(() => {
    if (!enabled) {
      setSession(null)
      setActorScope('')
      setLoading(false)
      return
    }
    void start()
  }, [enabled, start])
  return {
    session: enabled ? session : null,
    actorScope: enabled ? actorScope : '',
    loading,
    error,
    retry: start,
  }
}

export function useStoreGuestDisclaimer(token?: string) {
  const [data, setData] = useState<StoreDisclaimer | null>(null)
  const [error, setError] = useState<unknown>(null)
  const [loading, setLoading] = useState(false)
  const generation = useRef(0)
  const refetch = useCallback(async () => {
    const current = ++generation.current
    if (!token) return
    setLoading(true)
    setError(null)
    try {
      const result = await storeGuestApi.disclaimer(token)
      if (current === generation.current) setData(result)
    } catch (issue) {
      if (current === generation.current) {
        setData(null)
        setError(issue)
      }
    } finally {
      if (current === generation.current) setLoading(false)
    }
  }, [token])
  useEffect(() => {
    setData(null)
    void refetch()
    return () => {
      // eslint-disable-next-line react-hooks/exhaustive-deps -- Invalidates late network replies; this is not a DOM ref.
      generation.current++
    }
  }, [refetch])
  return {
    data,
    error,
    loading,
    refetch,
    accepted: () =>
      setData((current) =>
        current ? { ...current, accepted: true } : current
      ),
  }
}
