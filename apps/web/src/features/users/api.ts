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
import type { PermissionCatalog } from '@/lib/admin-permissions'
import { api } from '@/lib/api'
import type { CustomOAuthBinding } from '@/lib/oauth'

import type {
  User,
  GetUsersParams,
  GetUsersResponse,
  SearchUsersParams,
  UserFormData,
  ManageUserAction,
  ManageUserQuotaPayload,
  ManageUserTrustLevelPayload,
  ApiResponse,
  AccountActionRequestAdmin,
  AccountActionRequestStatus,
  AssistantRequestReviewListData,
} from './types'

export type DeveloperAccessRequestAdmin = {
  id: number
  user_id: number
  status: 'pending' | 'approved' | 'rejected'
  reason: string
  source:
    | 'assistant_recommendation'
    | 'user_edited'
    | 'assistant_request'
    | 'assistant_direct_grant'
    | 'legacy'
  ai_recommendation: string
  admin_user_id: number
  admin_note: string
  created_at: number
  reviewed_at: number
  username: string
  email: string
}

export type DeveloperAccessRecommendationArchive = {
  id: number
  user_id: number
  request_id: number
  source:
    | 'assistant_recommendation'
    | 'user_edited'
    | 'assistant_request'
    | 'assistant_direct_grant'
    | 'legacy'
  reason: string
  recommendation: string
  admin_user_id: number
  admin_note: string
  approved_at: number
  created_at: number
}

type DeveloperAccessRequestListResponse = ApiResponse<
  DeveloperAccessRequestAdmin[]
>

export async function listDeveloperAccessRequests(
  status: 'pending' | 'approved' | 'rejected' = 'pending'
): Promise<DeveloperAccessRequestListResponse> {
  const res = await api.get('/api/developer-access/requests', {
    params: { status },
  })
  return res.data
}

export async function reviewDeveloperAccessRequest(
  id: number,
  action: 'approve' | 'reject',
  note = ''
): Promise<ApiResponse<DeveloperAccessRequestAdmin>> {
  const res = await api.post(`/api/developer-access/requests/${id}/${action}`, {
    note,
  })
  return res.data
}

export async function listDeveloperAccessRecommendationArchives(
  userId: number
): Promise<ApiResponse<DeveloperAccessRecommendationArchive[]>> {
  const res = await api.get(`/api/user/${userId}/developer-access/archives`, {
    params: { limit: 50 },
  })
  return res.data
}

export async function listAccountActionRequests(
  status: AccountActionRequestStatus = 'pending'
): Promise<ApiResponse<AccountActionRequestAdmin[]>> {
  const res = await api.get('/api/account-action-requests', {
    params: { status },
  })
  return res.data
}

export async function reviewAccountActionRequest(
  id: number,
  action: 'approve' | 'reject',
  note = ''
): Promise<ApiResponse<AccountActionRequestAdmin>> {
  const res = await api.post(`/api/account-action-requests/${id}/${action}`, {
    note,
  })
  return res.data
}

// ============================================================================
// User Management APIs
// ============================================================================

/**
 * Get paginated users list
 */
export async function getUsers(
  params: GetUsersParams = {}
): Promise<GetUsersResponse> {
  const { p = 1, page_size = 10, trust_level, sort_by, sort_order } = params
  const res = await api.get('/api/user/', {
    params: {
      p,
      page_size,
      trust_level,
      sort_by,
      sort_order,
    },
  })
  return res.data
}

/**
 * Search users by keyword or group
 */
export async function searchUsers(
  params: SearchUsersParams
): Promise<GetUsersResponse> {
  const {
    keyword = '',
    group = '',
    role = '',
    status = '',
    trust_level,
    p = 1,
    page_size = 10,
    sort_by,
    sort_order,
  } = params
  const queryParams = new URLSearchParams()
  queryParams.set('keyword', keyword)
  queryParams.set('group', group)
  if (role) queryParams.set('role', role)
  if (status) queryParams.set('status', status)
  if (trust_level !== undefined) {
    queryParams.set('trust_level', String(trust_level))
  }
  queryParams.set('p', String(p))
  queryParams.set('page_size', String(page_size))
  if (sort_by) queryParams.set('sort_by', sort_by)
  if (sort_order) queryParams.set('sort_order', sort_order)
  const res = await api.get(`/api/user/search?${queryParams.toString()}`)
  return res.data
}

export async function listAssistantRequestReviews(
  userId: number,
  page = 1,
  pageSize = 20
): Promise<ApiResponse<AssistantRequestReviewListData>> {
  const res = await api.get('/api/assistant/admin/request-reviews', {
    params: { user_id: userId, page, page_size: pageSize },
  })
  return res.data
}

export async function resetAssistantRequestReviewViolations(
  userId: number
): Promise<
  ApiResponse<{ user_id: number; violation_count: number; reset_at: number }>
> {
  const res = await api.post(
    `/api/assistant/admin/users/${userId}/request-reviews/reset`
  )
  return res.data
}

/**
 * Get single user by ID
 */
export async function getUser(id: number): Promise<ApiResponse<User>> {
  const res = await api.get(`/api/user/${id}`)
  return res.data
}

export type AssistantUserProfile = {
  profile_key: string
  tags: string[]
  strategy: string
  enabled: boolean
  updated_at: number
}

export async function getAssistantUserProfile(
  id: number
): Promise<ApiResponse<AssistantUserProfile>> {
  const res = await api.get(`/api/user/${id}/assistant-profile`)
  return res.data
}

export async function updateAssistantUserProfile(
  id: number,
  profile: Pick<
    AssistantUserProfile,
    'profile_key' | 'tags' | 'strategy' | 'enabled'
  >
): Promise<ApiResponse<AssistantUserProfile>> {
  const res = await api.put(`/api/user/${id}/assistant-profile`, profile)
  return res.data
}

/**
 * Create a new user
 */
export async function createUser(
  data: UserFormData
): Promise<ApiResponse<User>> {
  const res = await api.post('/api/user/', data)
  return res.data
}

/**
 * Update an existing user
 */
export async function updateUser(
  data: UserFormData & { id: number }
): Promise<ApiResponse<Partial<User>>> {
  const res = await api.put('/api/user/', data)
  return res.data
}

/**
 * Delete a single user (hard delete)
 */
export async function deleteUser(id: number): Promise<ApiResponse> {
  const res = await api.delete(`/api/user/${id}/`)
  return res.data
}

/**
 * Manage user (promote, demote, enable, disable, delete)
 */
export async function manageUser(
  id: number,
  action: ManageUserAction
): Promise<ApiResponse<Partial<User>>> {
  const res = await api.post('/api/user/manage', { id, action })
  return res.data
}

/**
 * Adjust user quota atomically (add/subtract/override)
 */
export async function adjustUserQuota(
  payload: ManageUserQuotaPayload
): Promise<ApiResponse<Partial<User>>> {
  const res = await api.post('/api/user/manage', payload)
  return res.data
}

export async function setUserTrustLevel(
  payload: ManageUserTrustLevelPayload
): Promise<ApiResponse<Partial<User>>> {
  const res = await api.post('/api/user/manage', payload)
  return res.data
}

/**
 * Reset user's Passkey registration
 */
export async function resetUserPasskey(id: number): Promise<ApiResponse> {
  const res = await api.delete(`/api/user/${id}/reset_passkey`)
  return res.data
}

/**
 * Reset user's Two-Factor Authentication setup
 */
export async function resetUserTwoFA(id: number): Promise<ApiResponse> {
  const res = await api.delete(`/api/user/${id}/2fa`)
  return res.data
}

/**
 * Get all available groups
 */
export async function getGroups(): Promise<ApiResponse<string[]>> {
  const res = await api.get('/api/group/')
  return res.data
}

/**
 * Get the permission catalog (resources, actions, and role baselines).
 * Source of truth lives in the backend authz package.
 */
export async function getPermissionCatalog(): Promise<PermissionCatalog> {
  const res = await api.get('/api/authz/catalog')
  return {
    resources: res.data?.data?.resources ?? [],
    roles: res.data?.data?.roles ?? [],
  }
}

// ============================================================================
// Admin Binding Management APIs
// ============================================================================

export type OAuthBinding = CustomOAuthBinding

/**
 * Get user's custom OAuth bindings (admin)
 */
export async function getUserOAuthBindings(
  userId: number
): Promise<ApiResponse<OAuthBinding[]>> {
  const res = await api.get(`/api/user/${userId}/oauth/bindings`)
  return res.data
}

/**
 * Clear a user's built-in binding (admin)
 */
export async function adminClearUserBinding(
  userId: number,
  bindingType: string
): Promise<ApiResponse> {
  const res = await api.delete(`/api/user/${userId}/bindings/${bindingType}`)
  return res.data
}

/**
 * Unbind custom OAuth for a user (admin)
 */
export async function adminUnbindCustomOAuth(
  userId: number,
  providerId: number
): Promise<ApiResponse> {
  const res = await api.delete(
    `/api/user/${userId}/oauth/bindings/${providerId}`
  )
  return res.data
}
