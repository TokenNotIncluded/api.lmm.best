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
// ============================================================================
// Profile Type Definitions
// ============================================================================

/**
 * Generic API response
 */
export interface ApiResponse<T = unknown> {
  success: boolean
  message?: string
  data?: T
}

/**
 * User profile data
 */
export interface UserProfile {
  /** User ID */
  id: number
  /** Username */
  username: string
  /** Display name */
  display_name: string
  /** User role (1=普通用户, 10=管理员, 100=超级管理员) */
  role: number
  /** Email address */
  email?: string
  /** User group */
  group: string
  /** Current quota balance */
  quota: number
  /** Total used quota */
  used_quota: number
  /** Total request count */
  request_count: number
  /** Account status (1=启用, 2=禁用, 3=待审核, 4=已删除) */
  status: number
  /** Access token (system token) */
  access_token?: string
  /** Affiliate code */
  aff_code?: string
  /** Number of successful affiliate invites */
  aff_count: number
  /** Affiliate quota (pending rewards) */
  aff_quota: number
  /** Total affiliate quota earned (historical) */
  aff_history_quota: number
  /** Invite user ID */
  invite_user_id?: number
  /** Account creation timestamp */
  created_time: number
  /** User settings (JSON string) */
  setting?: string
  /** WeChat ID (OAuth) */
  wechat_id?: string
  /** GitHub ID (OAuth) */
  github_id?: string
  /** Discord ID (OAuth) */
  discord_id?: string
  /** OIDC ID (OAuth) */
  oidc_id?: string
  /** Telegram ID (OAuth) */
  telegram_id?: string
  /** LinuxDO ID (OAuth) */
  linux_do_id?: string
  /** Effective trust level returned by the authenticated self endpoint */
  trust_level_info?: import('@/stores/auth-store').TrustLevelInfo
}

/**
 * Notification type
 */
export type NotifyType = 'email' | 'webhook' | 'bark' | 'gotify'

export type UsageLeaderboardVisibility = 'public' | 'anonymous' | 'hidden'

export type FiatSettlementCurrency = 'CNY' | 'USD'

/** Empty means follow language; never send the literal "auto". */
export type SettlementCurrencyPreference = FiatSettlementCurrency | ''

/**
 * Parsed user settings
 */
export interface UserSettings {
  /** Notification type */
  notify_type?: NotifyType
  /** Quota warning threshold */
  quota_warning_threshold?: number
  /** Webhook URL */
  webhook_url?: string
  /** Webhook secret */
  webhook_secret?: string
  /** Notification email */
  notification_email?: string
  /** Bark URL */
  bark_url?: string
  /** Gotify server URL */
  gotify_url?: string
  /** Gotify application token */
  gotify_token?: string
  /** Gotify message priority (0-10) */
  gotify_priority?: number
  /** Accept unset model ratio model */
  accept_unset_model_ratio_model?: boolean
  /** Record IP log */
  record_ip_log?: boolean
  /** Receive upstream model update notifications (admin only) */
  upstream_model_update_notify_enabled?: boolean
  /** Preferred interface/API response language */
  language?: string
  /** Customer fiat preference, independent of quota display and credits */
  settlement_currency?: SettlementCurrencyPreference
  /** How this user's usage appears on the public leaderboard */
  usage_leaderboard_visibility?: UsageLeaderboardVisibility
}

/**
 * User update request
 */
export interface UpdateUserRequest {
  display_name?: string
  password?: string
  original_password?: string
}

/**
 * User settings update request
 */
export interface UpdateUserSettingsRequest {
  notify_type?: string
  quota_warning_threshold?: number
  webhook_url?: string
  webhook_secret?: string
  notification_email?: string
  bark_url?: string
  gotify_url?: string
  gotify_token?: string
  gotify_priority?: number
  accept_unset_model_ratio_model?: boolean
  record_ip_log?: boolean
  upstream_model_update_notify_enabled?: boolean
  usage_leaderboard_visibility?: UsageLeaderboardVisibility
}

/**
 * Account deletion request
 */
export interface DeleteAccountRequest {
  password?: string
}

/**
 * Account binding item
 */
export interface BindingItem {
  id: string
  label: string
  icon: React.ElementType
  value?: string
  isBound: boolean
  isEnabled: boolean
  onBind: () => void
  onUnbind?: () => void
}

/**
 * Two-Factor Authentication Status
 */
export interface TwoFAStatus {
  enabled: boolean
  locked: boolean
  backup_codes_remaining: number
}

/**
 * Two-Factor Authentication Setup Data
 */
export interface TwoFASetupData {
  secret: string
  qr_code_data: string
  backup_codes: string[]
}

// ============================================================================
// Compensation Gift Type Definitions
// ============================================================================

/**
 * A time-boxed compensation gift as shown to the current user.
 */
export interface GiftItem {
  id: number
  title: string
  description: string
  quota: number
  start_at: number
  end_at: number
  min_used_quota: number
  min_account_age_days: number
  enabled: boolean
  created_at: number
  /** Whether the current user already claimed this gift */
  claimed: boolean
  /** Claim unix timestamp when `claimed` is true */
  claimed_at?: number
  /** Whether the current user meets all eligibility gates right now */
  eligible: boolean
  /** Human-readable reason when `eligible` is false */
  reason?: string
}

/** Payload returned by POST /api/user/gift/:id/claim */
export interface GiftClaimResponse {
  claim: {
    id: number
    gift_id: number
    user_id: number
    quota: number
    created_at: number
  }
  already_claimed: boolean
}

// ============================================================================
// Checkin Type Definitions
// ============================================================================

/**
 * Checkin record for a specific date
 */
export interface CheckinRecord {
  /** Check-in date (YYYY-MM-DD) */
  checkin_date: string
  /** Quota awarded for this check-in */
  quota_awarded: number
}

/**
 * Checkin statistics
 */
export interface CheckinStats {
  /** Whether user has checked in today */
  checked_in_today: boolean
  /** Total number of check-ins */
  total_checkins: number
  /** Total quota earned from check-ins */
  total_quota: number
  /** Current month check-in count */
  checkin_count: number
  /** Check-in records for the queried month */
  records: CheckinRecord[]
}

/**
 * Check-in status response
 */
export interface CheckinStatusResponse {
  /** Whether check-in feature is enabled */
  enabled: boolean
  /** Effective reward range for the current user's trust level */
  min_quota?: number
  max_quota?: number
  /** Configured base range before trust-level scaling */
  base_min_quota?: number
  base_max_quota?: number
  /** Effective trust level and multiplier used for this user */
  trust_level?: number
  reward_multiplier?: number
  /** Check-in statistics */
  stats: CheckinStats
}

/**
 * Check-in action response
 */
export interface CheckinResponse {
  /** Quota awarded for this check-in */
  quota_awarded: number
}
