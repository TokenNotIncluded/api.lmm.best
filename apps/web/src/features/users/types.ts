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
import { z } from 'zod'

import type { AdminPermissionMatrix } from '@/lib/admin-permissions'
import type { TrustLevelInfo } from '@/stores/auth-store'

// ============================================================================
// User Schema & Types
// ============================================================================

/** User status: 1 = enabled, 2 = disabled, 3+ = other states */
export const userStatusSchema = z.number()
export type UserStatus = z.infer<typeof userStatusSchema>

/** User role: 1 = common user, 10 = admin, 100 = root */
export const userRoleSchema = z.number()
export type UserRole = z.infer<typeof userRoleSchema>

export const userSchema = z.object({
  id: z.number(),
  username: z.string(),
  display_name: z.string(),
  password: z.string().optional(),
  github_id: z.string().optional(),
  oidc_id: z.string().optional(),
  wechat_id: z.string().optional(),
  telegram_id: z.string().optional(),
  email: z.string().optional(),
  quota: z.number(),
  used_quota: z.number(),
  request_count: z.number(),
  group: z.string(),
  aff_code: z.string().optional(),
  aff_count: z.number().optional(),
  aff_quota: z.number().optional(),
  aff_history_quota: z.number().optional(),
  inviter_id: z.number().optional(),
  linux_do_id: z.string().optional(),
  status: userStatusSchema,
  role: userRoleSchema,
  created_at: z.number().optional(),
  updated_at: z.number().optional(),
  last_login_at: z.number().optional(),
  DeletedAt: z.any().nullable().optional(),
  remark: z.string().optional(),
  admin_permissions: z
    .record(z.string(), z.record(z.string(), z.boolean()))
    .optional(),
  trust_level_override: z.number().nullable().optional(),
  trust_level_info: z.any().optional(),
  payment_restriction_flags: z.number().optional(),
  disposable_email: z.boolean().optional(),
  linux_do_gamification_score: z.number().optional(),
  linux_do_score_updated_at: z.number().optional(),
  assistant_conversation_count: z.number().optional(),
  assistant_violation_count: z.number().optional(),
  assistant_profile: z
    .object({
      profile_key: z.string(),
      tags: z.array(z.string()),
      source: z.string(),
      updated_at: z.number(),
    })
    .optional(),
  topup_summary: z
    .object({
      quota: z.number(),
      money_micros: z.number(),
      currency: z.string().optional(),
      orders: z.number(),
      methods: z.array(
        z.object({
          method: z.string(),
          provider: z.string().optional(),
          settlement_currency: z.string().optional(),
          quota: z.number(),
          money_micros: z.number(),
          orders: z.number(),
        })
      ),
    })
    .optional(),
})
export type User = z.infer<typeof userSchema>

export const userListSchema = z.array(userSchema)

// ============================================================================
// API Request/Response Types
// ============================================================================

/** Generic API response */
export interface ApiResponse<T = unknown> {
  success: boolean
  message?: string
  data?: T
}

export type UserSortBy =
  | 'id'
  | 'username'
  | 'quota'
  | 'group'
  | 'created_at'
  | 'last_login_at'
  | 'topup_quota'
  | 'topup_money'
  | 'assistant_violations'

export type UserSortOrder = 'asc' | 'desc'

export interface GetUsersParams {
  p?: number
  page_size?: number
  trust_level?: number
  sort_by?: UserSortBy
  sort_order?: UserSortOrder
}

export interface GetUsersResponse {
  success: boolean
  message?: string
  data?: {
    items: User[]
    total: number
    page: number
    page_size: number
  }
}

export interface SearchUsersParams {
  keyword?: string
  group?: string
  role?: string
  status?: string
  trust_level?: number
  p?: number
  page_size?: number
  sort_by?: UserSortBy
  sort_order?: UserSortOrder
}

export interface AssistantRequestReviewAdmin {
  id: number
  user_id: number
  conversation_id: number
  request_id: string
  group: string
  review_model: string
  intensity: string
  status: string
  violation: boolean
  abuse: boolean
  rules: string[]
  explanation: string
  request_preview: string
  response_preview: string
  error_message: string
  created_at: number
  updated_at: number
}

export interface AssistantRequestReviewListData {
  items: AssistantRequestReviewAdmin[]
  total: number
  page: number
  page_size: number
  violation_count: number
  reset_at: number
}

export interface UserFormData {
  username: string
  display_name: string
  password?: string
  role?: number // Only used when creating user
  quota?: number // Only used when updating user
  group?: string // Only used when updating user
  remark?: string // Only used when updating user
  admin_permissions?: AdminPermissionMatrix
  trust_level_override?: number | null
}

export type ManageUserAction =
  | 'promote'
  | 'demote'
  | 'enable'
  | 'disable'
  | 'delete'
  | 'add_quota'
  | 'set_trust_level'
  | 'reset_onboarding'

export type QuotaAdjustMode = 'add' | 'subtract' | 'override'

export interface ManageUserQuotaPayload {
  id: number
  action: 'add_quota'
  mode: QuotaAdjustMode
  value: number
}

export interface ManageUserTrustLevelPayload {
  id: number
  action: 'set_trust_level'
  value: number
}

export type AccountActionRequestKind = 'disable' | 'appeal'
export type AccountActionRequestStatus = 'pending' | 'approved' | 'rejected'

export interface AccountActionRequestAdmin {
  id: number
  target_user_id: number
  requested_by_user_id: number
  kind: AccountActionRequestKind
  status: AccountActionRequestStatus
  reason: string
  admin_user_id: number
  admin_note: string
  created_at: number
  reviewed_at: number
  target_username: string
  target_email: string
  requested_by_username: string
  requested_by_email: string
}

export type UserTrustLevelInfo = TrustLevelInfo

// ============================================================================
// Dialog Types
// ============================================================================

export type UsersDialogType = 'create' | 'update' | 'delete'
