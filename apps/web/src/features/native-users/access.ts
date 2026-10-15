/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { createContext } from 'react'
import { z } from 'zod'

import { api, type ApiRequestConfig } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

export type NativeUsersScope = {
  userId: number
  sessionId: string
  role: typeof ROLE.ADMIN | typeof ROLE.SUPER_ADMIN
}

// A server denial revokes every private projection in the mounted workspace.
export const NativeUsersAccessBoundary = createContext<(() => void) | null>(
  null
)

export class NativeUsersFailure extends Error {
  constructor(
    readonly kind:
      | 'unavailable'
      | 'invalid_response'
      | 'invalid_query'
      | 'forbidden'
      | 'limited'
      | 'session_changed'
  ) {
    super(`User management: ${kind}`)
  }
}

export function readNativeUsersScope(
  auth = useAuthStore.getState().auth
): NativeUsersScope | undefined {
  const { user, session } = auth
  const role = user?.role
  if (
    !user ||
    !Number.isSafeInteger(user.id) ||
    user.id <= 0 ||
    typeof session?.sid !== 'string' ||
    !session.sid ||
    (role !== ROLE.ADMIN && role !== ROLE.SUPER_ADMIN)
  ) {
    return undefined
  }
  return { userId: user.id, sessionId: session.sid, role }
}

export function isCurrentNativeUsersScope(scope: NativeUsersScope): boolean {
  const current = readNativeUsersScope()
  return (
    current?.userId === scope.userId &&
    current.sessionId === scope.sessionId &&
    current.role === scope.role
  )
}

export function nativeUsersOptions(
  scope: NativeUsersScope,
  signal?: AbortSignal
): ApiRequestConfig {
  return {
    authScope: { userId: scope.userId, sessionId: scope.sessionId },
    signal,
    disableDuplicate: true,
    skipBusinessError: true,
    skipErrorHandler: true,
    skipAuthRefresh: true,
    timeout: 10000,
  }
}

export function nativeUsersFailure(error: unknown): NativeUsersFailure {
  if (error instanceof NativeUsersFailure) return error
  const response =
    typeof error === 'object' && error !== null && 'response' in error
      ? error.response
      : null
  const status =
    typeof response === 'object' && response !== null && 'status' in response
      ? response.status
      : null
  return new NativeUsersFailure(
    status === 401 || status === 403
      ? 'forbidden'
      : status === 429
        ? 'limited'
        : 'unavailable'
  )
}

const statusSchema = z.object({
  success: z.literal(true),
  data: z.object({
    admin_users_protocol: z.literal('core.admin-users.v1'),
    capabilities: z.object({ admin_users_read: z.boolean() }),
  }),
})

export function nativeUsersAvailable(payload: unknown): boolean {
  const parsed = statusSchema.safeParse(payload)
  return (
    parsed.success && parsed.data.data.capabilities.admin_users_read === true
  )
}

export async function readNativeUsersAvailability(
  scope: NativeUsersScope,
  signal?: AbortSignal
): Promise<boolean> {
  if (!isCurrentNativeUsersScope(scope)) {
    throw new NativeUsersFailure('session_changed')
  }
  try {
    const response = await api.get<unknown>('/api/status', {
      ...nativeUsersOptions(scope, signal),
      // A public capability probe must not start an auth refresh/redirect loop.
      skipAuthRefresh: true,
    })
    if (!isCurrentNativeUsersScope(scope) || signal?.aborted) {
      throw new NativeUsersFailure('session_changed')
    }
    return nativeUsersAvailable(response.data)
  } catch (error) {
    if (!isCurrentNativeUsersScope(scope) || signal?.aborted) {
      throw new NativeUsersFailure('session_changed')
    }
    throw nativeUsersFailure(error)
  }
}
