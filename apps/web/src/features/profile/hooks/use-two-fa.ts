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
import { useState, useEffect, useCallback, useRef } from 'react'

import { get2FAStatus } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import type { TwoFAStatus } from '../types'

// ============================================================================
// Two-FA Hook
// ============================================================================

export function useTwoFA(enabled = true) {
  const [loading, setLoading] = useState(true)
  const [status, setStatus] = useState<TwoFAStatus | null>(null)
  const [error, setError] = useState(false)
  const request = useRef(0)
  const invalidate = useCallback(() => {
    request.current++
  }, [])
  const userId = useAuthStore((state) => state.auth.user?.id)
  const sessionId = useAuthStore((state) => state.auth.session?.sid)

  const fetchStatus = useCallback(async () => {
    const attempt = ++request.current
    if (!enabled) return

    try {
      setLoading(true)
      setError(false)
      setStatus(null)
      const response = await get2FAStatus({
        skipErrorHandler: true,
        skipBusinessError: true,
        authScope: { userId, sessionId },
      })
      if (attempt !== request.current) return
      const data = response.data
      if (
        response.success !== true ||
        !data ||
        typeof data.enabled !== 'boolean' ||
        typeof data.locked !== 'boolean' ||
        !Number.isSafeInteger(data.backup_codes_remaining) ||
        data.backup_codes_remaining < 0
      ) {
        throw new Error('Invalid two-factor status')
      }
      setStatus(data)
    } catch {
      if (attempt === request.current) {
        setStatus(null)
        setError(true)
      }
    } finally {
      if (attempt === request.current) setLoading(false)
    }
  }, [enabled, userId, sessionId])

  useEffect(() => {
    fetchStatus()
    return invalidate
  }, [fetchStatus, invalidate])

  return {
    status,
    loading,
    error,
    refetch: fetchStatus,
  }
}
