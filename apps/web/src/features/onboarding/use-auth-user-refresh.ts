/*
Copyright (C) 2023-2026 QuantumNous

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

For commercial licensing, please contact support@quantumnous.com
*/
import { useCallback, useEffect, useRef } from 'react'

import { getSelf } from '@/lib/api'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'

export async function refreshCurrentAccount(): Promise<AuthUser | null> {
  const before = useAuthStore.getState().auth
  if (!before.user) return null
  try {
    const response = await getSelf()
    const after = useAuthStore.getState().auth
    if (
      after.user?.id !== before.user.id ||
      after.session?.sid !== before.session?.sid
    ) {
      return null
    }
    if (
      response?.success &&
      response.data &&
      response.data.id === before.user.id
    ) {
      const user = response.data as AuthUser
      after.setUser(user)
      return user
    }
  } catch {
    // Keep the last known account on transient failure; never overwrite a new session.
  }
  return null
}

export function useAuthUserRefresh() {
  const inFlightRef = useRef<Promise<AuthUser | null> | null>(null)

  const refreshUser = useCallback(() => {
    if (inFlightRef.current) return inFlightRef.current

    const request = refreshCurrentAccount()

    inFlightRef.current = request
    void request.finally(() => {
      if (inFlightRef.current === request) {
        inFlightRef.current = null
      }
    })

    return request
  }, [])

  useEffect(() => {
    void refreshUser()

    const handleFocus = () => {
      void refreshUser()
    }
    const handleVisibilityChange = () => {
      if (document.visibilityState === 'visible') {
        void refreshUser()
      }
    }

    window.addEventListener('focus', handleFocus)
    document.addEventListener('visibilitychange', handleVisibilityChange)

    return () => {
      window.removeEventListener('focus', handleFocus)
      document.removeEventListener('visibilitychange', handleVisibilityChange)
    }
  }, [refreshUser])

  return { refreshUser }
}
