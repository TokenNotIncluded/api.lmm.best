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
import { create } from 'zustand'

import type { AdminCapabilities } from '@/lib/admin-permissions'

export type UserPermissions = {
  sidebar_settings?: boolean
  sidebar_modules?: Record<string, unknown>
  admin_permissions?: AdminCapabilities
  console_activated_at?: number
  docs_access?: boolean
}

export interface TrustLevelInfo {
  level: number
  automatic_level: number
  override_level: number | null
  paid_amount: number
  discount_ratio: number
  discount_percent: number
  next_level?: number | null
  next_level_paid_amount?: number | null
  amount_to_next_level?: number | null
  next_decay_at?: number | null
  inactivity_decay_steps: number
  decay_period_days: number
  overridden: boolean
}

export interface TrustLevelTier {
  level: number
  min_paid_amount: number
  discount_percent: number
  benefits?: string[]
  benefit_count?: number
  benefits_hidden?: boolean
  discount_hidden?: boolean
}

export type OnboardingStage =
  | 'activate'
  | 'credential'
  | 'first_request'
  | 'complete'

export interface OnboardingState {
  paid_activation_enabled?: boolean
  paid_activation_min_amount?: number
  paid_activation_complete?: boolean
  details_available?: boolean
  activation_complete: boolean
  api_key_created?: boolean
  credential_complete: boolean
  first_request_complete: boolean
  stage: OnboardingStage
}

export interface AuthUser {
  id: number
  username: string
  display_name?: string
  email?: string
  role: number
  status?: number
  group?: string
  quota?: number
  used_quota?: number
  request_count?: number
  aff_code?: string
  aff_count?: number
  aff_quota?: number
  aff_debt?: number
  aff_history_quota?: number
  inviter_id?: number
  github_id?: string
  discord_id?: string
  oidc_id?: string
  wechat_id?: string
  telegram_id?: string
  linux_do_id?: string
  language?: string
  setting?: Record<string, unknown> | string
  stripe_customer?: string
  sidebar_modules?: string
  permissions?: UserPermissions
  /** Authoritative server decision for developer access. Unknown means denied. */
  developer_access_granted?: boolean
  trust_level_info?: TrustLevelInfo
  trust_level_tiers?: TrustLevelTier[]
  // The nested state is the current API contract. Flat fields remain optional
  // while locally running binaries transition to the nested response shape.
  onboarding?: OnboardingState
  activation_complete?: boolean
  credential_complete?: boolean
  first_request_complete?: boolean
  onboarding_stage?: OnboardingStage
}

export interface LoginSession {
  sid: string
  current: boolean
  login_method: string
  ip: string
  user_agent: string
  created_at: number
  last_active_at: number
  expires_at: number
}

export interface AuthBundle {
  access_token: string
  token_type: 'Bearer' | string
  access_expires_at: number
  user: AuthUser
  session: LoginSession
}

export type AuthBootstrapState = 'idle' | 'checking' | 'complete'

interface AuthState {
  auth: {
    user: AuthUser | null
    accessToken: string | null
    accessExpiresAt: number | null
    session: LoginSession | null
    pending2FAFlowToken: string | null
    bootstrapState: AuthBootstrapState
    setBundle: (bundle: AuthBundle) => void
    setUser: (user: AuthUser | null) => void
    setPending2FAFlowToken: (flowToken: string | null) => void
    setBootstrapState: (bootstrapState: AuthBootstrapState) => void
    reset: (bootstrapState?: AuthBootstrapState) => void
  }
}

export const useAuthStore = create<AuthState>()((set) => ({
  auth: {
    user: null,
    accessToken: null,
    accessExpiresAt: null,
    session: null,
    pending2FAFlowToken: null,
    bootstrapState: 'idle',
    setBundle: (bundle) =>
      set((state) => ({
        ...state,
        auth: {
          ...state.auth,
          user: bundle.user,
          accessToken: bundle.access_token,
          accessExpiresAt: bundle.access_expires_at,
          session: bundle.session,
          pending2FAFlowToken: null,
          bootstrapState: 'complete',
        },
      })),
    setUser: (user) =>
      set((state) => ({
        ...state,
        auth: { ...state.auth, user },
      })),
    setPending2FAFlowToken: (pending2FAFlowToken) =>
      set((state) => ({
        ...state,
        auth: { ...state.auth, pending2FAFlowToken },
      })),
    setBootstrapState: (bootstrapState) =>
      set((state) => ({
        ...state,
        auth: { ...state.auth, bootstrapState },
      })),
    reset: (bootstrapState = 'complete') =>
      set((state) => ({
        ...state,
        auth: {
          ...state.auth,
          user: null,
          accessToken: null,
          accessExpiresAt: null,
          session: null,
          pending2FAFlowToken: null,
          bootstrapState,
        },
      })),
  },
}))
