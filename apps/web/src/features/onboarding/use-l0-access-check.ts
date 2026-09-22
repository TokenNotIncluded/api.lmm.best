/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useEffect, useRef, useState } from 'react'

import { useAuthStore } from '@/stores/auth-store'

import { watchL0Access, type L0AccessCheckState } from './l0-paid-access'
import { refreshCurrentAccount } from './use-auth-user-refresh'

export function useL0AccessCheck(userId: number | undefined) {
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  const [state, setState] = useState<L0AccessCheckState | null>(null)
  const cancel = useRef<(() => void) | undefined>(undefined)
  const active = useRef(false)

  useEffect(() => {
    active.current = true
    return () => {
      active.current = false
      cancel.current?.()
    }
  }, [userId, sessionId])

  const check = () => {
    if (!userId) return
    cancel.current?.()
    cancel.current = watchL0Access({
      userId,
      read: refreshCurrentAccount,
      report: (next) => {
        if (active.current) setState(next)
      },
    })
  }

  return { state, check }
}
