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
import { api } from '@/lib/api'
import type { CustomOAuthBinding } from '@/lib/oauth'
import type { LoginSession } from '@/stores/auth-store'

import type { ProfileUsageQueryRange, ProfileUsageRow } from './lib/activity'
import type {
  ApiResponse,
  UserProfile,
  UpdateUserRequest,
  UpdateUserSettingsRequest,
  DeleteAccountRequest,
  CheckinStatusResponse,
  CheckinResponse,
  GiftItem,
  GiftClaimResponse,
} from './types'

// ============================================================================
// User Profile APIs
// ============================================================================

/**
 * Get current user profile
 */
export async function getUserProfile(): Promise<ApiResponse<UserProfile>> {
  const res = await api.get('/api/user/self')
  return res.data
}

/**
 * Fetch one bounded window of the current user's token activity.
 */
export async function getProfileUsageWindow(
  params: ProfileUsageQueryRange
): Promise<ProfileUsageRow[]> {
  const res = await api.get<ApiResponse<ProfileUsageRow[]>>('/api/data/self', {
    params: { ...params, default_time: 'day' },
    skipBusinessError: true,
    skipErrorHandler: true,
  })

  if (!res.data.success) {
    throw new Error(res.data.message || 'Unable to load profile activity')
  }
  return res.data.data ?? []
}

/**
 * Update user profile
 */
export async function updateUserProfile(
  data: UpdateUserRequest
): Promise<ApiResponse> {
  const res = await api.put('/api/user/self', data, {
    acceptAuthRotation: Boolean(data.password),
  })
  return res.data
}

/**
 * Update user settings
 */
export async function updateUserSettings(
  data: UpdateUserSettingsRequest
): Promise<ApiResponse> {
  const res = await api.put('/api/user/setting', data)
  return res.data
}

/**
 * Update interface language preference
 */
export async function updateUserLanguage(
  language: string
): Promise<ApiResponse> {
  const res = await api.put('/api/user/self', { language })
  return res.data
}

/**
 * Delete user account
 */
export async function deleteUserAccount(
  data?: DeleteAccountRequest
): Promise<ApiResponse> {
  const res = await api.delete('/api/user/self', { data })
  return res.data
}

// ============================================================================
// Account Binding APIs
// ============================================================================

/**
 * Send email verification code
 */
export async function sendEmailVerification(
  email: string,
  turnstileToken?: string
): Promise<ApiResponse> {
  const params = new URLSearchParams({ email })
  if (turnstileToken) {
    params.append('turnstile', turnstileToken)
  }
  const res = await api.get(`/api/verification?${params}`)
  return res.data
}

/**
 * Bind email account
 */
export async function bindEmail(
  email: string,
  code: string
): Promise<ApiResponse> {
  const res = await api.post('/api/oauth/email/bind', {
    email,
    code,
  })
  return res.data
}

/**
 * Bind WeChat account
 */
export async function bindWeChat(code: string): Promise<ApiResponse> {
  const res = await api.post(
    '/api/oauth/wechat/bind',
    { code },
    { skipBusinessError: true, skipErrorHandler: true }
  )
  return res.data
}

export interface TelegramBindFlow {
  flow_token: string
  callback_url: string
  expires_at: number
}

export async function startTelegramBind(): Promise<
  ApiResponse<TelegramBindFlow>
> {
  const res = await api.post('/api/oauth/telegram/bind/start')
  return res.data
}

// ============================================================================
// Login Session APIs
// ============================================================================

export async function getLoginSessions(): Promise<ApiResponse<LoginSession[]>> {
  const res = await api.get('/api/user/sessions')
  return res.data
}

export async function revokeLoginSession(sid: string): Promise<ApiResponse> {
  const res = await api.delete(`/api/user/sessions/${encodeURIComponent(sid)}`)
  return res.data
}

export async function revokeOtherLoginSessions(): Promise<ApiResponse> {
  const res = await api.post('/api/user/sessions/revoke-others')
  return res.data
}

// ============================================================================
// Custom OAuth Binding APIs
// ============================================================================

export type { CustomOAuthBinding } from '@/lib/oauth'

/**
 * Get current user's custom OAuth bindings
 */
export async function getSelfOAuthBindings(): Promise<
  ApiResponse<CustomOAuthBinding[]>
> {
  const res = await api.get('/api/user/oauth/bindings')
  return res.data
}

/**
 * Unbind a custom OAuth provider for current user
 */
export async function unbindCustomOAuth(
  providerId: number
): Promise<ApiResponse> {
  const res = await api.delete(`/api/user/oauth/bindings/${providerId}`)
  return res.data
}

/**
 * Unbind a built-in OAuth provider for the current user
 */
export async function unbindBuiltInOAuth(
  bindingType: string
): Promise<ApiResponse> {
  const res = await api.delete(
    `/api/user/bindings/${encodeURIComponent(bindingType)}`
  )
  return res.data
}

// ============================================================================
// Checkin APIs
// ============================================================================

/**
 * Get checkin status for a specific month
 */
export async function getCheckinStatus(
  month: string
): Promise<ApiResponse<CheckinStatusResponse>> {
  const res = await api.get(`/api/user/checkin?month=${month}`)
  return res.data
}

/**
 * Perform daily checkin
 */
export async function performCheckin(
  turnstileToken?: string
): Promise<ApiResponse<CheckinResponse>> {
  const url = turnstileToken
    ? `/api/user/checkin?turnstile=${encodeURIComponent(turnstileToken)}`
    : '/api/user/checkin'
  const res = await api.post(url, undefined, {
    // The check-in card presents verification failures itself so that the
    // challenge flow does not produce a duplicate global toast.
    skipBusinessError: true,
  })
  return res.data
}

// ============================================================================
// Compensation Gift APIs
// ============================================================================

/**
 * List gifts currently visible to the user with claim/eligibility status
 */
export async function getAvailableGifts(): Promise<ApiResponse<GiftItem[]>> {
  const res = await api.get('/api/user/gift')
  return res.data
}

/**
 * Claim a compensation gift. Idempotent: repeated claims return 200 with
 * `already_claimed=true` and quota is credited exactly once.
 */
export async function claimGift(
  giftId: number
): Promise<ApiResponse<GiftClaimResponse>> {
  const res = await api.post(`/api/user/gift/${giftId}/claim`)
  return res.data
}
