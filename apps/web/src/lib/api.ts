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
import { api, type ApiRequestConfig } from '@/lib/http-client'

export {
  applyAuthBundle,
  applyAuthRotation,
  bindAuthCache,
  bootstrapAuthentication,
  clearAuthenticatedClientState,
  clearAuthentication,
  getCommonHeaders,
  getFreshAuthHeaders,
  isAuthBundle,
  refreshAuthentication,
  AuthRotationError,
} from '@/lib/auth-session'
export type { AuthTokenRotation, RefreshOutcome } from '@/lib/auth-session'
export { api }
export type { ApiRequestConfig } from '@/lib/http-client'

// ============================================================================
// User APIs
// ============================================================================

export async function getSelf() {
  const res = await api.get('/api/user/self', {
    skipErrorHandler: true,
  })
  return res.data
}

export async function getUserModels(): Promise<{
  success: boolean
  message?: string
  data?: string[]
}> {
  const res = await api.get('/api/user/models')
  return res.data
}

export async function getUserGroups(): Promise<{
  success: boolean
  message?: string
  data?: Record<
    string,
    {
      desc: string
      ratio: number | string
      warning?: {
        enabled: boolean
        message: string
        mode: 'modal' | 'banner' | 'inline'
        confirmations: number
      }
    }
  >
}> {
  const res = await api.get('/api/user/self/groups')
  return res.data
}

// ============================================================================
// System APIs
// ============================================================================

export async function getStatus() {
  const res = await api.get('/api/status', {
    timeout: 10_000,
    // Capability probing is public. An expired anonymous session must not
    // trigger refresh/redirect logic before registration can render.
    skipAuthRefresh: true,
    // Capability probing is best-effort; callers render their own fallback.
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  return extractStatusData(res.data)
}

export function extractStatusData(payload: unknown): Record<string, unknown> {
  const data =
    typeof payload === 'object' && payload !== null && 'data' in payload
      ? (payload as { data?: unknown }).data
      : undefined
  if (!data || typeof data !== 'object' || Array.isArray(data)) {
    throw new Error('Status response did not include capability data')
  }
  return data as Record<string, unknown>
}

export async function getNotice(): Promise<{
  success: boolean
  message?: string
  data?: string
}> {
  const res = await api.get('/api/notice', {
    // Notices are optional decoration; an unavailable API should not toast.
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  return res.data
}

// ============================================================================
// 2FA Management APIs
// ============================================================================

export async function get2FAStatus(
  config: Pick<ApiRequestConfig, 'skipErrorHandler'> = {}
) {
  const res = await api.get('/api/user/2fa/status', config)
  return res.data
}

export async function setup2FA() {
  const res = await api.post('/api/user/2fa/setup')
  return res.data
}

export async function enable2FA(code: string) {
  const res = await api.post(
    '/api/user/2fa/enable',
    { code },
    { acceptAuthRotation: true }
  )
  return res.data
}

export async function disable2FA(code: string) {
  const res = await api.post(
    '/api/user/2fa/disable',
    { code },
    { acceptAuthRotation: true }
  )
  return res.data
}

export async function regenerate2FABackupCodes(code: string) {
  const res = await api.post(
    '/api/user/2fa/backup_codes',
    { code },
    { acceptAuthRotation: true }
  )
  return res.data
}
