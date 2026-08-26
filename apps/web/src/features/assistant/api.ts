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
import axios, { type AxiosError, type AxiosResponse } from 'axios'

import type { QuotaDataItem } from '@/features/dashboard/types'
import type { PricingData } from '@/features/pricing/types'
import type { PlanRecord } from '@/features/subscriptions/types'
import { api, getCommonHeaders, getFreshAuthHeaders } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import {
  AssistantStreamError,
  consumeAssistantAISDKStream,
} from './assistant-ai-stream'
import { redactAssistantMessageForRequest } from './assistant-message-safety'

type AssistantChatPayload = {
  choices?: Array<{
    message?: {
      content?: string
    }
  }>
  error?: {
    message?: string
    code?: string
  }
  code?: string
  message?: string
  lmm_assistant_action?: unknown
  lmm_assistant_policy?: unknown
  lmm_assistant_history?: {
    conversation_id?: unknown
    privacy_notice?: unknown
    restricted?: unknown
  }
  lmm_assistant_tools?: unknown
  retryable?: boolean
}

export type AssistantChatMessage = {
  role: 'user' | 'assistant'
  content: string
}

const ASSISTANT_CONVERSATION_MAX_ITEMS = 12
const ASSISTANT_CONVERSATION_MAX_RUNES = 12_000
const ASSISTANT_MESSAGE_MAX_RUNES = 4_000
export const ASSISTANT_MAX_REQUEST_ATTEMPTS = 5
const ASSISTANT_RETRY_DELAYS_MS = [200, 500, 1_000, 1_500] as const

type AssistantStreamHandlers = {
  onDelta?: (content: string) => void
  onReset?: () => void
}

function isRetryableAssistantError(error: unknown): boolean {
  if (isAssistantRequestAborted(error)) return false
  if (axios.isAxiosError(error)) {
    const status = error.response?.status
    return (
      status === undefined ||
      status === 408 ||
      status === 425 ||
      status === 429 ||
      status >= 500
    )
  }
  if (error instanceof AssistantStreamError) {
    return error.retryable
  }
  // A failed fetch has no HTTP status. It is safe to retry because the
  // browser has not received a completed assistant response.
  return error instanceof TypeError
}

export function isAssistantRequestAborted(error: unknown): boolean {
  return error instanceof Error && error.name === 'AbortError'
}

function waitForAssistantRetry(delayMs: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, delayMs))
}

export type AssistantFundingStatus = {
  mode: 'super_administrator'
}

export type AssistantPreConversationPreset = {
  id: string
  prompt: string
  label?: string
}

export type AssistantPreConversationPresets = {
  generation: number
  version: string
  presets: AssistantPreConversationPreset[]
}

export type AssistantStatus = {
  enabled: boolean
  model: string
  group?: string
  route_available?: boolean
  funding: AssistantFundingStatus
  developer_access_granted: boolean
  access_level?: string
  trust_level?: number
  role?: number
  is_admin?: boolean
  is_root?: boolean
  capabilities?: {
    public_assistant?: boolean
    account?: boolean
    developer_tools?: boolean
    usage_discount?: boolean
    admin_config?: boolean
    admin_pricing?: boolean
    admin_model_inventory?: boolean
    admin_model_sync?: boolean
  }
}

export type L1OnboardingStepId =
  | 'create_api_key'
  | 'install_client'
  | 'configure_client'
  | 'first_successful_response'

export type L1OnboardingTodo = {
  eligibility: {
    eligible: boolean
    developer_access_granted: boolean
    trust_level: number
    reason?: string
  }
  status: 'unavailable' | 'in_progress' | 'completed'
  current_step?: L1OnboardingStepId
  steps: Array<{
    id: L1OnboardingStepId
    status: 'pending' | 'completed'
    completed_at?: number
  }>
  completed_at?: number
}

export type AssistantJourneyStepId =
  | 'ask_ai'
  | 'get_recommendation'
  | 'create_api_key'
  | 'install_client'
  | 'configure_client'
  | 'first_api_call'
  | 'earn_ai_gift'
  | 'accept_bounty'

export type AssistantJourney = {
  main: Array<{
    id: AssistantJourneyStepId
    status: 'pending' | 'completed' | 'failed'
  }>
  side: Array<{
    id: AssistantJourneyStepId
    status: 'pending' | 'completed' | 'failed'
  }>
}

export type AssistantNewUserGift = {
  amount_cents: number
  quota: number
  status: 'offered' | 'claimed' | 'declined'
  reason: string
  created_at: number
  claimed_at: number
}

/**
 * The server emits this lightweight action when the assistant has prepared a
 * gift for the user to claim. It intentionally does not include quota or any
 * private account fields; those remain available only through the signed-in
 * gift endpoint.
 */
export type AssistantNewUserGiftAction = {
  type: 'new_user_gift'
  amount_cents: number
  status: 'offered'
  reason: string
}

export type AssistantWeeklyDiscount = {
  discount_percent: number
  status: 'offered' | 'claimed' | 'declined'
  reason: string
  week_start: number
  created_at: number
  claimed_at?: number
  code?: string
}

export type AssistantWeeklyDiscountAction = {
  type: 'weekly_discount'
  discount_percent: number
  status: 'offered'
  reason: string
}

export type AssistantL1RecommendationAction = {
  type: 'l1_recommendation'
  user_statement: string
  recommendation: string
  confirmation_token: string
}

export type AssistantAccountDisableAction = {
  type: 'account_disable_request'
  target_user_id: number
  target_username: string
  reason: string
  confirmation_token: string
}

export type AssistantHumanSupportAction = {
  type: 'human_support'
  confirmation_token: string
  requires_confirmation: true
  expires_in_seconds: number
  message: string
}

export type AssistantCreateKeyAction = {
  type: 'create_key'
  confirmation_token: string
  requires_confirmation: true
  expires_in_seconds: number
  name: string
  group: string
  conversation_id?: number
  ui_path?: string
}

export type AssistantImageGenerationAction = {
  type: 'image_generation'
  confirmation_token: string
  requires_confirmation: true
  expires_in_seconds: number
  prompt: string
  model: string
  group: string
  n: number
  size?: string
  quality?: string
}

export type AssistantAdminConfigPreview = {
  key: string
  label: string
  old_value: string
  new_value: string
}

export type AssistantAdminPricingPreview = {
  model_id: string
  old: Record<string, unknown>
  next: Record<string, unknown>
}

export type AssistantAdminConfigChangeAction = {
  type: 'admin_config_change'
  confirmation_token: string
  requires_confirmation: true
  expires_in_seconds: number
  scope?: 'user_skills' | 'channel'
  target_user_id?: number
  channel_id?: number
  channel_name?: string
  changes: AssistantAdminConfigPreview[]
}

export type AssistantAdminPricingChangeAction = {
  type: 'admin_pricing_change'
  confirmation_token: string
  requires_confirmation: true
  expires_in_seconds: number
  pricing: AssistantAdminPricingPreview
}

export type AssistantAdminModelSyncPreview = {
  model_id: string
  vendor: string
  status: number
}

export type AssistantAdminModelSyncAction = {
  type: 'admin_model_sync'
  confirmation_token: string
  requires_confirmation: true
  expires_in_seconds: number
  models: AssistantAdminModelSyncPreview[]
  vendors?: Array<{
    name: string
    description?: string
    icon?: string
    status: number
  }>
  locale: string
  source_digest: string
}

export type AssistantAdminChangeAction =
  | AssistantAdminConfigChangeAction
  | AssistantAdminPricingChangeAction
  | AssistantAdminModelSyncAction

export type AssistantNavigationPath =
  | '/'
  | '/getting-started'
  | '/pricing'
  | '/wallet'
  | '/usage-logs/common'
  | '/usage-logs/drawing'
  | '/usage-logs/task'
  | '/keys'
  | '/drawing'
  | '/models'
  | '/profile'
  | '/support'
  | '/open-source-bounties'
  | '/users'

export type AssistantNavigationAction = {
  type: 'navigate'
  path: AssistantNavigationPath
  query: Record<string, string | number | boolean>
}

type AssistantUserTargetAction = {
  requires_confirmation: true
  target_user_id: number
  target_username: string
  target_display_name: string
  target_role: number
  target_group: string
  target_is_self: boolean
}

export type AssistantUserPasswordChangeAction = AssistantUserTargetAction & {
  type: 'user_password_change'
}

export type AssistantUserOAuthUnbindAction = AssistantUserTargetAction & {
  type: 'user_oauth_unbind'
  provider: string
  provider_kind: 'built_in' | 'custom'
  provider_label: string
}

export type AssistantUserAccountAction = AssistantUserTargetAction & {
  type: 'user_account_action'
  action: 'disable' | 'delete'
}

export type AssistantUserAction =
  | AssistantUserPasswordChangeAction
  | AssistantUserOAuthUnbindAction
  | AssistantUserAccountAction

export type AssistantToolTrace = {
  name: string
  status: 'output-available' | 'output-error' | 'approval-requested'
  input?: Record<string, string | number | boolean>
  result?: number
  errorCode?: 'missing_math_expression' | 'invalid_math_expression'
}

export type AssistantAction =
  | AssistantL1RecommendationAction
  | AssistantAccountDisableAction
  | AssistantHumanSupportAction
  | AssistantCreateKeyAction
  | AssistantNewUserGiftAction
  | AssistantWeeklyDiscountAction
  | AssistantImageGenerationAction
  | AssistantAdminChangeAction
  | AssistantNavigationAction
  | AssistantUserAction

export type AssistantCreatedKey = {
  id: number
  name: string
  group: string
  expired_time: number
  card: AssistantPrivateCard
}

export type AssistantPrivateCard = {
  id: string
  label?: string
}

export type AssistantSecureCardView = {
  id?: string
  type?: string
  label?: string
  owner: 'self' | 'protected'
  shield: boolean
}

export type AssistantConversationHistoryMessage = {
  id: number
  role: 'user' | 'assistant' | 'secure_card'
  content: string
  created_at: number
  cards?: AssistantSecureCardView[]
}

export type AssistantConversationHistorySummary = {
  id: number
  title: string
  last_message_preview: string
  created_at: number
  updated_at: number
  archived_at: number
  restricted_at?: number
  owner: 'self' | 'lower_level_user'
  privacy_notice: string
}

export type AssistantConversationHistoryItem =
  AssistantConversationHistorySummary

export type AssistantConversationHistory = {
  conversations: AssistantConversationHistorySummary[]
  next_cursor?: string
  privacy_notice?: string
}

export type AssistantConversationHistoryDetail = {
  conversation: AssistantConversationHistorySummary
  messages: AssistantConversationHistoryMessage[]
  privacy_notice: string
}

export type AssistantConversationArchiveResult = {
  id: number
  archived: boolean
  archived_at: number
}

export type AssistantHandoff = {
  id: number
  user_id: number
  source: 'handoff'
  intent: 'human_support'
  message: string
  status: 'pending' | 'resolved'
  admin_user_id: number
  admin_note: string
  created_at: number
  resolved_at: number
  username?: string
  email?: string
}

export type AssistantIntent =
  | 'onboarding'
  | 'plan_purchase'
  | 'api_key'
  | 'client_setup'
  | 'cost'
  | 'math'
  | 'recommendation'
  | 'bounty'
  | 'usage'
  | 'models'
  | 'invitation'
  | 'human_support'
  | 'other'

export type AssistantIntentSummary = {
  intent: AssistantIntent
  count: number
}

export type AssistantProfileSummary = {
  profile: string
  count: number
}

export type AssistantFirstQuestionSummary = {
  question: string
  count: number
  last_asked_at: number
}

export type AssistantFundingSummary = {
  start_timestamp: number
  end_timestamp: number
  requests: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  quota: number
  cost_usd: number
  remaining_quota: number
  remaining_usd: number
}

export type AssistantReply = {
  content: string
  intent?: AssistantIntent
  action?: AssistantAction
  conversationId?: number
  restricted?: boolean
  tools?: AssistantToolTrace[]
}

export type AssistantPlanOffers = {
  ok: boolean
  developer_access_granted: boolean
  read_only: boolean
  checkout_available: boolean
  payment_hidden: boolean
  payment_compliance_confirmed: boolean
  plans: PlanRecord[]
  topup_discounts: Record<string, number>
  message?: string
  next_step?: string
  error?: string
}

const ASSISTANT_INTENTS = new Set<AssistantIntent>([
  'onboarding',
  'plan_purchase',
  'api_key',
  'client_setup',
  'cost',
  'math',
  'recommendation',
  'bounty',
  'usage',
  'models',
  'invitation',
  'human_support',
  'other',
])

type AssistantAPIResponse<T> = {
  success: boolean
  data?: T
  message?: string
  code?: string
}

export class AssistantRequestError extends Error {
  readonly code?: string

  constructor(message: string, code?: string) {
    super(message)
    this.name = 'AssistantRequestError'
    this.code = code
  }
}

export type AssistantErrorInfo = {
  code?: string
  message?: string
  status?: number
}

export function getAssistantErrorInfo(error: unknown): AssistantErrorInfo {
  if (!error || typeof error !== 'object') {
    return {}
  }
  const candidate = error as {
    message?: unknown
    response?: { status?: unknown; data?: unknown }
  }
  const responseData = candidate.response?.data
  const payload =
    responseData && typeof responseData === 'object'
      ? (responseData as { code?: unknown; message?: unknown })
      : undefined
  let message: string | undefined
  if (typeof payload?.message === 'string') message = payload.message
  else if (typeof candidate.message === 'string') message = candidate.message
  return {
    ...(typeof payload?.code === 'string' ? { code: payload.code } : {}),
    ...(message ? { message } : {}),
    ...(typeof candidate.response?.status === 'number'
      ? { status: candidate.response.status }
      : {}),
  }
}

function requireAssistantData<T>(
  payload: AssistantAPIResponse<T>,
  fallback: string
): T {
  if (!payload.success || payload.data === undefined) {
    throw new AssistantRequestError(payload.message || fallback, payload.code)
  }
  return payload.data
}

function normalizeAssistantRequestError(
  error: AxiosError<AssistantAPIResponse<never>>,
  fallback: string
) {
  const payload = error.response?.data
  return new AssistantRequestError(payload?.message || fallback, payload?.code)
}

export function parseAssistantReply(payload: AssistantChatPayload): string {
  const content = payload.choices?.[0]?.message?.content?.trim()
  if (content) return content
  throw new Error(
    payload.error?.message || payload.message || 'Assistant returned no answer'
  )
}

function buildAssistantReply(
  payload: AssistantChatPayload,
  intentHeader?: unknown
): AssistantReply {
  const tools = parseAssistantToolTraces(payload.lmm_assistant_tools)
  const responseConversationId = payload.lmm_assistant_history?.conversation_id
  const conversationRestricted =
    payload.lmm_assistant_history?.restricted === true ||
    payload.lmm_assistant_policy === 'security_refusal' ||
    payload.lmm_assistant_policy === 'conversation_restricted'
  const reply: AssistantReply = {
    content: parseAssistantReply(payload),
    intent: parseAssistantIntent(intentHeader),
    action: parseAssistantAction(payload.lmm_assistant_action),
    ...(tools.length > 0 ? { tools } : {}),
  }
  if (
    typeof responseConversationId === 'number' &&
    Number.isSafeInteger(responseConversationId) &&
    responseConversationId > 0
  ) {
    reply.conversationId = responseConversationId
  }
  if (conversationRestricted) reply.restricted = true
  return reply
}

export function parseAssistantIntent(
  value: unknown
): AssistantIntent | undefined {
  if (typeof value !== 'string') return undefined
  const intent = value.trim().toLowerCase() as AssistantIntent
  return ASSISTANT_INTENTS.has(intent) ? intent : undefined
}

const ASSISTANT_NAVIGATION_PATHS = new Set<AssistantNavigationPath>([
  '/',
  '/getting-started',
  '/pricing',
  '/wallet',
  '/usage-logs/common',
  '/usage-logs/drawing',
  '/usage-logs/task',
  '/keys',
  '/drawing',
  '/models',
  '/profile',
  '/support',
  '/open-source-bounties',
  '/users',
])

const ASSISTANT_NAVIGATION_QUERY_KEYS: Record<
  AssistantNavigationPath,
  readonly string[]
> = {
  '/': [],
  '/getting-started': [],
  '/pricing': [],
  '/wallet': [],
  '/usage-logs/common': ['username'],
  '/usage-logs/drawing': ['username'],
  '/usage-logs/task': ['username'],
  '/keys': [],
  '/drawing': [],
  '/models': [],
  '/profile': [],
  '/support': [],
  '/open-source-bounties': [],
  '/users': ['filter', 'l0Only'],
}

function parseAssistantNavigationAction(
  action: Record<string, unknown>
): AssistantNavigationAction | undefined {
  if (action.type !== 'navigate' || typeof action.path !== 'string') {
    return undefined
  }
  const path = action.path.trim() as AssistantNavigationPath
  if (!ASSISTANT_NAVIGATION_PATHS.has(path)) return undefined
  const queryValue = action.query
  const query: Record<string, string | number | boolean> = {}
  if (queryValue !== undefined) {
    if (!queryValue || typeof queryValue !== 'object') return undefined
    const allowedKeys = ASSISTANT_NAVIGATION_QUERY_KEYS[path]
    for (const [key, value] of Object.entries(
      queryValue as Record<string, unknown>
    )) {
      if (!allowedKeys.includes(key)) return undefined
      if (
        typeof value !== 'string' &&
        typeof value !== 'number' &&
        typeof value !== 'boolean'
      ) {
        return undefined
      }
      if (typeof value === 'string' && value.trim().length > 200) {
        return undefined
      }
      if (typeof value === 'number' && !Number.isFinite(value)) {
        return undefined
      }
      query[key] = typeof value === 'string' ? value.trim() : value
    }
  }
  return { type: 'navigate', path, query }
}

function parseAssistantNewUserGiftAction(
  action: Record<string, unknown>
): AssistantNewUserGiftAction | undefined {
  if (
    action.type !== 'new_user_gift' ||
    action.status !== 'offered' ||
    typeof action.amount_cents !== 'number' ||
    !Number.isInteger(action.amount_cents) ||
    action.amount_cents < 1 ||
    action.amount_cents > 1000 ||
    typeof action.reason !== 'string'
  ) {
    return undefined
  }
  const reason = action.reason.trim()
  const reasonRunes = [...reason].length
  if (reasonRunes < 2 || reasonRunes > 240) return undefined
  return {
    type: 'new_user_gift',
    amount_cents: action.amount_cents,
    status: 'offered',
    reason,
  }
}

function parseAssistantWeeklyDiscountAction(
  action: Record<string, unknown>
): AssistantWeeklyDiscountAction | undefined {
  if (
    action.type !== 'weekly_discount' ||
    action.status !== 'offered' ||
    typeof action.discount_percent !== 'number' ||
    !Number.isInteger(action.discount_percent) ||
    action.discount_percent < 1 ||
    action.discount_percent > 10 ||
    typeof action.reason !== 'string'
  ) {
    return undefined
  }
  const reason = action.reason.trim()
  const reasonRunes = [...reason].length
  if (reasonRunes < 2 || reasonRunes > 240) return undefined
  return {
    type: 'weekly_discount',
    discount_percent: action.discount_percent,
    status: 'offered',
    reason,
  }
}

function parseAssistantUserAction(
  action: Record<string, unknown>
): AssistantUserAction | undefined {
  if (
    'password' in action ||
    'new_password' in action ||
    'current_password' in action ||
    'api_key' in action ||
    'access_token' in action
  ) {
    return undefined
  }
  if (
    action.requires_confirmation !== true ||
    typeof action.target_user_id !== 'number' ||
    !Number.isInteger(action.target_user_id) ||
    action.target_user_id < 1 ||
    typeof action.target_username !== 'string' ||
    typeof action.target_display_name !== 'string' ||
    typeof action.target_role !== 'number' ||
    !Number.isInteger(action.target_role) ||
    typeof action.target_group !== 'string' ||
    typeof action.target_is_self !== 'boolean'
  ) {
    return undefined
  }
  const targetUsername = action.target_username.trim()
  const targetDisplayName = action.target_display_name.trim()
  const targetGroup = action.target_group.trim()
  if (!targetUsername || !targetGroup) return undefined
  const target = {
    requires_confirmation: true as const,
    target_user_id: action.target_user_id,
    target_username: targetUsername,
    target_display_name: targetDisplayName,
    target_role: action.target_role,
    target_group: targetGroup,
    target_is_self: action.target_is_self,
  }
  if (action.type === 'user_password_change') {
    return { type: action.type, ...target }
  }
  if (
    action.type === 'user_oauth_unbind' &&
    (action.provider_kind === 'built_in' ||
      action.provider_kind === 'custom') &&
    typeof action.provider === 'string' &&
    typeof action.provider_label === 'string'
  ) {
    const provider = action.provider.trim()
    const providerLabel = action.provider_label.trim()
    if (!provider || !providerLabel) return undefined
    return {
      type: action.type,
      ...target,
      provider,
      provider_kind: action.provider_kind,
      provider_label: providerLabel,
    }
  }
  if (
    action.type === 'user_account_action' &&
    (action.action === 'disable' || action.action === 'delete')
  ) {
    return { type: action.type, ...target, action: action.action }
  }
  return undefined
}

export function parseAssistantToolTraces(value: unknown): AssistantToolTrace[] {
  if (!Array.isArray(value)) return []
  return value
    .slice(0, 12)
    .map((item) => {
      if (!item || typeof item !== 'object') return null
      const trace = item as Record<string, unknown>
      if (
        typeof trace.name !== 'string' ||
        !['output-available', 'output-error', 'approval-requested'].includes(
          trace.status as string
        )
      ) {
        return null
      }
      const name = trace.name.trim()
      if (!name || name.length > 80) return null
      let input: AssistantToolTrace['input']
      if (trace.input !== undefined) {
        if (!trace.input || typeof trace.input !== 'object') return null
        input = {}
        for (const [key, rawValue] of Object.entries(
          trace.input as Record<string, unknown>
        )) {
          if (
            typeof rawValue !== 'string' &&
            typeof rawValue !== 'number' &&
            typeof rawValue !== 'boolean'
          ) {
            return null
          }
          if (typeof rawValue === 'string' && rawValue.length > 200) {
            return null
          }
          if (typeof rawValue === 'number' && !Number.isFinite(rawValue)) {
            return null
          }
          input[key] = rawValue
        }
      }
      const result =
        name === 'calculate_math' &&
        typeof trace.result === 'number' &&
        Number.isFinite(trace.result)
          ? trace.result
          : undefined
      const errorCode =
        name === 'calculate_math' &&
        (trace.error_code === 'missing_math_expression' ||
          trace.error_code === 'invalid_math_expression')
          ? trace.error_code
          : undefined
      return {
        name,
        status: trace.status as AssistantToolTrace['status'],
        ...(input && Object.keys(input).length > 0 ? { input } : {}),
        ...(result !== undefined ? { result } : {}),
        ...(errorCode ? { errorCode } : {}),
      }
    })
    .filter((trace): trace is AssistantToolTrace => trace !== null)
}

export function parseAssistantAction(
  value: unknown
): AssistantAction | undefined {
  if (!value || typeof value !== 'object') return undefined
  const action = value as Record<string, unknown>
  const navigation = parseAssistantNavigationAction(action)
  if (navigation) return navigation
  const newUserGift = parseAssistantNewUserGiftAction(action)
  if (newUserGift) return newUserGift
  const weeklyDiscount = parseAssistantWeeklyDiscountAction(action)
  if (weeklyDiscount) return weeklyDiscount
  const userAction = parseAssistantUserAction(action)
  if (userAction) return userAction
  const confirmationToken =
    typeof action.confirmation_token === 'string'
      ? action.confirmation_token.trim()
      : ''
  if (!confirmationToken) return undefined

  if (
    action.type === 'human_support' &&
    action.requires_confirmation === true &&
    typeof action.expires_in_seconds === 'number' &&
    Number.isInteger(action.expires_in_seconds) &&
    action.expires_in_seconds > 0 &&
    typeof action.message === 'string'
  ) {
    const message = action.message.trim()
    const messageRunes = [...message].length
    if (messageRunes >= 5 && messageRunes <= 2000) {
      return {
        type: 'human_support',
        confirmation_token: confirmationToken,
        requires_confirmation: true,
        expires_in_seconds: action.expires_in_seconds,
        message,
      }
    }
  }

  if (
    action.type === 'admin_config_change' &&
    action.requires_confirmation === true &&
    typeof action.expires_in_seconds === 'number' &&
    Number.isInteger(action.expires_in_seconds) &&
    action.expires_in_seconds > 0 &&
    Array.isArray(action.changes)
  ) {
    const changes = action.changes
      .map((item) => {
        if (!item || typeof item !== 'object') return null
        const change = item as Record<string, unknown>
        if (
          typeof change.key !== 'string' ||
          typeof change.label !== 'string' ||
          typeof change.old_value !== 'string' ||
          typeof change.new_value !== 'string'
        ) {
          return null
        }
        const key = change.key.trim()
        const label = change.label.trim()
        if (!key || !label) return null
        return {
          key,
          label,
          old_value: change.old_value,
          new_value: change.new_value,
        }
      })
      .filter(
        (change): change is AssistantAdminConfigPreview => change !== null
      )
    if (changes.length === action.changes.length && changes.length > 0) {
      const scope =
        action.scope === 'user_skills' || action.scope === 'channel'
          ? action.scope
          : undefined
      const targetUserID =
        typeof action.target_user_id === 'number' &&
        Number.isInteger(action.target_user_id) &&
        action.target_user_id > 0
          ? action.target_user_id
          : undefined
      const channelID =
        typeof action.channel_id === 'number' &&
        Number.isInteger(action.channel_id) &&
        action.channel_id > 0
          ? action.channel_id
          : undefined
      const channelName =
        typeof action.channel_name === 'string'
          ? action.channel_name.trim()
          : ''
      if (
        (action.scope !== undefined && scope === undefined) ||
        (action.target_user_id !== undefined && targetUserID === undefined) ||
        (action.channel_id !== undefined && channelID === undefined) ||
        (action.channel_name !== undefined && !channelName)
      ) {
        return undefined
      }
      return {
        type: 'admin_config_change',
        confirmation_token: confirmationToken,
        requires_confirmation: true,
        expires_in_seconds: action.expires_in_seconds,
        ...(scope ? { scope } : {}),
        ...(targetUserID ? { target_user_id: targetUserID } : {}),
        ...(channelID ? { channel_id: channelID } : {}),
        ...(channelName ? { channel_name: channelName } : {}),
        changes,
      }
    }
  }

  if (
    action.type === 'admin_pricing_change' &&
    action.requires_confirmation === true &&
    typeof action.expires_in_seconds === 'number' &&
    Number.isInteger(action.expires_in_seconds) &&
    action.expires_in_seconds > 0 &&
    action.pricing &&
    typeof action.pricing === 'object'
  ) {
    const pricing = action.pricing as Record<string, unknown>
    if (
      typeof pricing.model_id === 'string' &&
      pricing.old &&
      typeof pricing.old === 'object' &&
      pricing.next &&
      typeof pricing.next === 'object'
    ) {
      const modelId = pricing.model_id.trim()
      if (modelId) {
        return {
          type: 'admin_pricing_change',
          confirmation_token: confirmationToken,
          requires_confirmation: true,
          expires_in_seconds: action.expires_in_seconds,
          pricing: {
            model_id: modelId,
            old: pricing.old as Record<string, unknown>,
            next: pricing.next as Record<string, unknown>,
          },
        }
      }
    }
  }

  if (
    action.type === 'admin_model_sync' &&
    action.requires_confirmation === true &&
    typeof action.expires_in_seconds === 'number' &&
    Number.isInteger(action.expires_in_seconds) &&
    action.expires_in_seconds > 0 &&
    Array.isArray(action.models) &&
    typeof action.locale === 'string' &&
    typeof action.source_digest === 'string'
  ) {
    const models = action.models
      .map((item) => {
        if (!item || typeof item !== 'object') return null
        const model = item as Record<string, unknown>
        if (
          typeof model.model_id !== 'string' ||
          typeof model.vendor !== 'string' ||
          typeof model.status !== 'number' ||
          !Number.isInteger(model.status)
        ) {
          return null
        }
        const modelId = model.model_id.trim()
        const vendor = model.vendor.trim()
        return modelId
          ? { model_id: modelId, vendor, status: model.status }
          : null
      })
      .filter((item): item is AssistantAdminModelSyncPreview => item !== null)
    const locale = action.locale.trim()
    const sourceDigest = action.source_digest.trim().toLowerCase()
    const vendors = Array.isArray(action.vendors)
      ? action.vendors
          .map((item) => {
            if (!item || typeof item !== 'object') return null
            const vendor = item as Record<string, unknown>
            if (
              typeof vendor.name !== 'string' ||
              typeof vendor.status !== 'number' ||
              !Number.isInteger(vendor.status)
            ) {
              return null
            }
            const name = vendor.name.trim()
            return name
              ? {
                  name,
                  ...(typeof vendor.description === 'string'
                    ? { description: vendor.description.trim() }
                    : {}),
                  ...(typeof vendor.icon === 'string'
                    ? { icon: vendor.icon.trim() }
                    : {}),
                  status: vendor.status,
                }
              : null
          })
          .filter((item) => item !== null)
      : []
    if (
      models.length === action.models.length &&
      models.length > 0 &&
      (action.vendors === undefined ||
        action.vendors === null ||
        (Array.isArray(action.vendors) &&
          vendors.length === action.vendors.length)) &&
      /^[0-9a-f]{64}$/.test(sourceDigest)
    ) {
      return {
        type: 'admin_model_sync',
        confirmation_token: confirmationToken,
        requires_confirmation: true,
        expires_in_seconds: action.expires_in_seconds,
        models,
        ...(vendors.length ? { vendors } : {}),
        locale,
        source_digest: sourceDigest,
      }
    }
  }

  if (
    action.type === 'create_key' &&
    action.requires_confirmation === true &&
    typeof action.expires_in_seconds === 'number' &&
    Number.isInteger(action.expires_in_seconds) &&
    action.expires_in_seconds > 0 &&
    typeof action.name === 'string' &&
    typeof action.group === 'string'
  ) {
    const name = action.name.trim()
    const group = action.group.trim()
    if (name && group) {
      return {
        type: 'create_key',
        confirmation_token: confirmationToken,
        requires_confirmation: true,
        expires_in_seconds: action.expires_in_seconds,
        name,
        group,
      }
    }
  }

  if (
    action.type === 'image_generation' &&
    action.requires_confirmation === true &&
    typeof action.expires_in_seconds === 'number' &&
    Number.isInteger(action.expires_in_seconds) &&
    action.expires_in_seconds > 0 &&
    typeof action.prompt === 'string' &&
    typeof action.model === 'string' &&
    typeof action.group === 'string' &&
    typeof action.n === 'number' &&
    Number.isInteger(action.n) &&
    action.n >= 1 &&
    action.n <= 4
  ) {
    const prompt = action.prompt.trim()
    const model = action.model.trim()
    const group = action.group.trim()
    const size = typeof action.size === 'string' ? action.size.trim() : ''
    const quality =
      typeof action.quality === 'string' ? action.quality.trim() : ''
    if (
      prompt &&
      prompt.length <= 2000 &&
      model &&
      model.length <= 200 &&
      group &&
      group.length <= 64 &&
      (!size || size.length <= 32) &&
      (!quality || quality.length <= 32)
    ) {
      return {
        type: 'image_generation',
        confirmation_token: confirmationToken,
        requires_confirmation: true,
        expires_in_seconds: action.expires_in_seconds,
        prompt,
        model,
        group,
        n: action.n,
        ...(size ? { size } : {}),
        ...(quality ? { quality } : {}),
      }
    }
  }

  if (
    action.type === 'l1_recommendation' &&
    typeof action.user_statement === 'string' &&
    typeof action.recommendation === 'string'
  ) {
    const userStatement = action.user_statement.trim()
    const recommendation = action.recommendation.trim()
    if (!userStatement || !recommendation) return undefined
    return {
      type: 'l1_recommendation',
      user_statement: userStatement,
      recommendation,
      confirmation_token: confirmationToken,
    }
  }

  if (
    action.type === 'account_disable_request' &&
    typeof action.target_user_id === 'number' &&
    Number.isInteger(action.target_user_id) &&
    action.target_user_id > 0 &&
    typeof action.target_username === 'string' &&
    typeof action.reason === 'string'
  ) {
    const targetUsername = action.target_username.trim()
    const reason = action.reason.trim()
    if (!targetUsername || !reason) return undefined
    return {
      type: 'account_disable_request',
      target_user_id: action.target_user_id,
      target_username: targetUsername,
      reason,
      confirmation_token: confirmationToken,
    }
  }
  return undefined
}

function normalizedAssistantHistoryMessage(
  message: AssistantChatMessage
): AssistantChatMessage | null {
  const content = redactAssistantMessageForRequest(
    message.content
  ).content.trim()
  if (!content) return null
  return {
    role: message.role,
    content: [...content].slice(0, ASSISTANT_MESSAGE_MAX_RUNES).join(''),
  }
}

export function buildAssistantConversation(
  history: AssistantChatMessage[],
  currentMessage: string
): AssistantChatMessage[] {
  const current = currentMessage.trim()
  const currentRunes = [...current]
  if (!current || currentRunes.length > ASSISTANT_MESSAGE_MAX_RUNES) {
    throw new Error(
      `Assistant message must be between 1 and ${ASSISTANT_MESSAGE_MAX_RUNES} characters`
    )
  }

  const conversation: AssistantChatMessage[] = [
    { role: 'user', content: current },
  ]
  let totalRunes = currentRunes.length
  for (let index = history.length - 1; index >= 0; index -= 1) {
    if (conversation.length >= ASSISTANT_CONVERSATION_MAX_ITEMS) break
    const message = normalizedAssistantHistoryMessage(history[index])
    if (!message) continue
    const messageRunes = [...message.content].length
    if (totalRunes + messageRunes > ASSISTANT_CONVERSATION_MAX_RUNES) break
    conversation.unshift(message)
    totalRunes += messageRunes
  }

  while (conversation[0]?.role === 'assistant') conversation.shift()
  return conversation
}

function buildAssistantRequestBody(
  normalizedMessage: string,
  messages: AssistantChatMessage[],
  conversationId?: number,
  presetId?: string
): Record<string, unknown> {
  return {
    message: normalizedMessage,
    messages,
    ...(conversationId && conversationId > 0
      ? { conversation_id: conversationId }
      : {}),
    ...(presetId ? { preset_id: presetId } : {}),
  }
}

async function readAssistantFetchPayload(
  response: Response
): Promise<AssistantChatPayload> {
  const text = await response.text()
  if (!text.trim()) return {}
  try {
    return JSON.parse(text) as AssistantChatPayload
  } catch {
    return { message: text.trim() }
  }
}

async function consumeAssistantStream(
  body: ReadableStream<Uint8Array>,
  handlers: AssistantStreamHandlers
): Promise<AssistantChatPayload> {
  return (await consumeAssistantAISDKStream(
    body,
    handlers
  )) as AssistantChatPayload
}

async function sendAssistantMessageStream(
  body: Record<string, unknown>,
  attempt: number,
  handlers: AssistantStreamHandlers,
  signal?: AbortSignal
): Promise<AssistantReply> {
  const auth = useAuthStore.getState().auth
  const authHeaders =
    auth.user && auth.session ? await getFreshAuthHeaders() : getCommonHeaders()
  const response = await fetch('/api/assistant/chat', {
    method: 'POST',
    credentials: 'include',
    headers: {
      ...authHeaders,
      Accept: 'text/event-stream',
      'X-LMM-Assistant-Attempt': String(attempt),
    },
    signal,
    body: JSON.stringify(body),
  })

  const payload = response.ok
    ? undefined
    : await readAssistantFetchPayload(response)
  if (!response.ok) {
    const message =
      payload?.error?.message ||
      payload?.message ||
      `Assistant request failed with HTTP ${response.status}`
    throw new AssistantStreamError(
      response.status,
      payload,
      message,
      payload?.retryable === true || response.status >= 500
    )
  }

  const contentType = response.headers.get('content-type')?.toLowerCase() || ''
  if (!contentType.includes('text/event-stream')) {
    const jsonPayload = payload ?? (await readAssistantFetchPayload(response))
    return buildAssistantReply(
      jsonPayload,
      response.headers.get('x-lmm-assistant-intent')
    )
  }
  if (!response.body) {
    throw new AssistantStreamError(
      502,
      { message: 'Assistant stream body is unavailable' },
      'Assistant stream body is unavailable'
    )
  }
  const streamedPayload = await consumeAssistantStream(response.body, handlers)
  return buildAssistantReply(
    streamedPayload,
    response.headers.get('x-lmm-assistant-intent')
  )
}

export async function sendAssistantMessage(
  message: string,
  history: AssistantChatMessage[] = [],
  conversationId?: number,
  presetId?: string,
  handlers?: AssistantStreamHandlers,
  signal?: AbortSignal
): Promise<AssistantReply> {
  const normalizedMessage =
    redactAssistantMessageForRequest(message).content.trim()
  const messages = buildAssistantConversation(history, normalizedMessage)
  const requestBody = buildAssistantRequestBody(
    normalizedMessage,
    messages,
    conversationId,
    presetId
  )
  let response: AxiosResponse<AssistantChatPayload> | undefined
  for (
    let attempt = 1;
    attempt <= ASSISTANT_MAX_REQUEST_ATTEMPTS;
    attempt += 1
  ) {
    if (attempt > 1) handlers?.onReset?.()
    try {
      if (handlers?.onDelta) {
        return await sendAssistantMessageStream(
          requestBody,
          attempt,
          handlers,
          signal
        )
      }
      response = await api.post<AssistantChatPayload>(
        '/api/assistant/chat',
        requestBody,
        {
          skipBusinessError: true,
          skipErrorHandler: true,
          headers: { 'X-LMM-Assistant-Attempt': String(attempt) },
        }
      )
      break
    } catch (error) {
      if (
        !isRetryableAssistantError(error) ||
        attempt >= ASSISTANT_MAX_REQUEST_ATTEMPTS
      ) {
        throw error
      }
      await waitForAssistantRetry(
        ASSISTANT_RETRY_DELAYS_MS[attempt - 1] ?? 1_500
      )
    }
  }
  if (!response) throw new Error('Assistant request did not complete')
  return buildAssistantReply(
    response.data,
    response.headers['x-lmm-assistant-intent']
  )
}

export async function getAssistantPreConversationPresets(): Promise<AssistantPreConversationPresets> {
  const response = await api.get<
    AssistantAPIResponse<AssistantPreConversationPresets>
  >('/api/assistant/pre-conversation-presets')
  return requireAssistantData(
    response.data,
    'Unable to load assistant conversation starters'
  )
}

export async function recordAssistantPreConversationPresetClick(
  presetId: string
): Promise<void> {
  await api.post(
    `/api/assistant/pre-conversation-presets/${encodeURIComponent(presetId)}/click`,
    undefined,
    { skipBusinessError: true, skipErrorHandler: true }
  )
}

export async function executeAssistantUserAction(
  action: AssistantUserAction,
  input: { currentPassword?: string; newPassword?: string }
): Promise<{ selfDeleted: boolean }> {
  const skipOptions = {
    skipBusinessError: true,
    skipErrorHandler: true,
  } as const
  if (action.type === 'user_password_change') {
    const password = input.newPassword ?? ''
    if (action.target_is_self) {
      const response = await api.put<AssistantAPIResponse<unknown>>(
        '/api/user/self',
        { original_password: input.currentPassword ?? '', password },
        { ...skipOptions, acceptAuthRotation: true }
      )
      requireAssistantData(response.data, 'Unable to change the password')
    } else {
      const response = await api.put<AssistantAPIResponse<unknown>>(
        '/api/user/',
        {
          id: action.target_user_id,
          username: action.target_username,
          display_name: action.target_display_name,
          role: action.target_role,
          group: action.target_group,
          password,
        },
        skipOptions
      )
      requireAssistantData(response.data, 'Unable to change the password')
    }
    return { selfDeleted: false }
  }
  if (action.type === 'user_oauth_unbind') {
    const base = action.target_is_self
      ? '/api/user'
      : `/api/user/${action.target_user_id}`
    const path =
      action.provider_kind === 'custom'
        ? `${base}/oauth/bindings/${encodeURIComponent(action.provider)}`
        : `${base}/bindings/${encodeURIComponent(action.provider)}`
    const response = await api.delete<AssistantAPIResponse<unknown>>(
      path,
      skipOptions
    )
    requireAssistantData(response.data, 'Unable to unbind the OAuth login')
    return { selfDeleted: false }
  }
  if (action.target_is_self) {
    if (action.action !== 'delete') {
      throw new Error(
        'This account action is not available for the current user'
      )
    }
    const response = await api.delete<AssistantAPIResponse<unknown>>(
      '/api/user/self',
      { ...skipOptions }
    )
    requireAssistantData(response.data, 'Unable to delete the account')
    return { selfDeleted: true }
  }
  const response = await api.post<AssistantAPIResponse<unknown>>(
    '/api/user/manage',
    { id: action.target_user_id, action: action.action },
    skipOptions
  )
  requireAssistantData(
    response.data,
    action.action === 'delete'
      ? 'Unable to delete the user'
      : 'Unable to disable the user'
  )
  return { selfDeleted: false }
}

export async function submitAssistantAccountDisableRequest(input: {
  target_user_id: number
  reason: string
  confirmation_token: string
}): Promise<unknown> {
  const response = await api.post<AssistantAPIResponse<unknown>>(
    '/api/user/account-action-requests',
    { ...input, confirmed: true },
    { skipBusinessError: true, skipErrorHandler: true }
  )
  return requireAssistantData(
    response.data,
    'Unable to submit the account safety request'
  )
}

export async function submitAssistantAdminChange(
  confirmationToken: string
): Promise<{ applied: boolean; kind: string }> {
  const response = await api.post<
    AssistantAPIResponse<{ applied: boolean; kind: string }>
  >(
    '/api/assistant/admin/apply',
    { confirmation_token: confirmationToken, confirmed: true },
    { skipBusinessError: true, skipErrorHandler: true }
  )
  return requireAssistantData(
    response.data,
    'Unable to apply the administrator change'
  )
}

export type AssistantGeneratedImage = {
  url?: string
  b64_json?: string
  revised_prompt?: string
}

type AssistantImageGenerationResponse = {
  data?: AssistantGeneratedImage[]
  error?: { message?: string }
  message?: string
}

export async function generateAssistantImage(
  confirmationToken: string
): Promise<AssistantGeneratedImage[]> {
  const response = await api.post<AssistantImageGenerationResponse>(
    '/api/assistant/drawing/generate',
    { confirmation_token: confirmationToken },
    { skipBusinessError: true, skipErrorHandler: true }
  )
  const payload = response.data
  if (payload.error || !Array.isArray(payload.data)) {
    throw new Error(
      payload.error?.message ||
        payload.message ||
        'Unable to generate the image'
    )
  }
  return payload.data.filter((item): item is AssistantGeneratedImage =>
    Boolean(
      item &&
      typeof item === 'object' &&
      (typeof item.url === 'string' || typeof item.b64_json === 'string')
    )
  )
}

export async function getAssistantStatus(): Promise<AssistantStatus> {
  const response = await api.get<AssistantAPIResponse<AssistantStatus>>(
    '/api/assistant/status',
    {
      disableDuplicate: true,
      skipBusinessError: true,
      skipErrorHandler: true,
    }
  )
  return requireAssistantData(response.data, 'Unable to load assistant status')
}

export async function getAssistantAvailableModels(): Promise<string[]> {
  const response = await api.get<AssistantAPIResponse<string[]>>(
    '/api/user/models',
    {
      skipBusinessError: true,
      skipErrorHandler: true,
    }
  )
  return requireAssistantData(response.data, 'Unable to load available models')
}

export async function getAssistantPricing(): Promise<PricingData> {
  const response = await api.get<PricingData>('/api/assistant/pricing', {
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  if (!response.data.success) {
    throw new Error(response.data.message || 'Unable to load live pricing')
  }
  return response.data
}

export async function getAssistantPlanOffers(): Promise<AssistantPlanOffers> {
  const response = await api.get<AssistantAPIResponse<AssistantPlanOffers>>(
    '/api/assistant/offers',
    {
      skipBusinessError: true,
      skipErrorHandler: true,
    }
  )
  const offers = requireAssistantData(
    response.data,
    'Unable to load live plan offers'
  )
  if (!offers.ok) {
    throw new Error(offers.error || 'Unable to load live plan offers')
  }
  return offers
}

export async function getAssistantUsageData(
  days: 7 | 30 | 90
): Promise<QuotaDataItem[]> {
  const endTimestamp = Math.floor(Date.now() / 1000)
  const response = await api.get<AssistantAPIResponse<QuotaDataItem[]>>(
    '/api/data/self',
    {
      params: {
        start_timestamp: endTimestamp - days * 24 * 60 * 60,
        end_timestamp: endTimestamp,
        default_time: 'day',
      },
      skipBusinessError: true,
      skipErrorHandler: true,
    }
  )
  return requireAssistantData(response.data, 'Failed to fetch usage')
}

export async function prepareAssistantDefaultKey(
  name: string,
  group: string,
  groupWarningConfirmations = 0
): Promise<AssistantCreateKeyAction> {
  try {
    const response = await api.post<
      AssistantAPIResponse<AssistantCreateKeyAction>
    >(
      '/api/assistant/tools/prepare-key',
      {
        name,
        group,
        group_warning_confirmations: groupWarningConfirmations,
      },
      { skipBusinessError: true, skipErrorHandler: true }
    )
    return requireAssistantData(response.data, 'Unable to prepare API key')
  } catch (error) {
    if (axios.isAxiosError<AssistantAPIResponse<never>>(error)) {
      throw normalizeAssistantRequestError(error, 'Unable to prepare API key')
    }
    throw error
  }
}

export async function confirmAssistantDefaultKey(
  confirmationToken: string,
  twoFactorCode = ''
): Promise<AssistantCreatedKey> {
  try {
    const response = await api.post<AssistantAPIResponse<AssistantCreatedKey>>(
      '/api/assistant/tools/create-key',
      {
        confirmation_token: confirmationToken,
        two_factor_code: twoFactorCode.trim(),
      },
      { skipBusinessError: true, skipErrorHandler: true }
    )
    return requireAssistantData(response.data, 'Unable to create API key')
  } catch (error) {
    if (axios.isAxiosError<AssistantAPIResponse<never>>(error)) {
      throw normalizeAssistantRequestError(error, 'Unable to create API key')
    }
    throw error
  }
}

export async function getL1OnboardingTodo(): Promise<L1OnboardingTodo> {
  const response = await api.get<AssistantAPIResponse<L1OnboardingTodo>>(
    '/api/user/self/onboarding/todo',
    {
      disableDuplicate: true,
      skipBusinessError: true,
      skipErrorHandler: true,
    }
  )
  return requireAssistantData(response.data, 'Unable to load setup checklist')
}

export async function getAssistantJourney(): Promise<AssistantJourney> {
  const response = await api.get<AssistantAPIResponse<AssistantJourney>>(
    '/api/assistant/journey',
    {
      disableDuplicate: true,
      skipBusinessError: true,
      skipErrorHandler: true,
    }
  )
  return requireAssistantData(response.data, 'Unable to load task progress')
}

export async function getAssistantNewUserGift(): Promise<AssistantNewUserGift | null> {
  const response = await api.get<
    AssistantAPIResponse<AssistantNewUserGift | null>
  >('/api/assistant/new-user-gift', {
    disableDuplicate: true,
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  return requireAssistantData(response.data, 'Unable to load welcome gift')
}

export async function claimAssistantNewUserGift(): Promise<{
  gift: AssistantNewUserGift
  already_claimed: boolean
}> {
  const response = await api.post<
    AssistantAPIResponse<{
      gift: AssistantNewUserGift
      already_claimed: boolean
    }>
  >('/api/assistant/new-user-gift/claim', undefined, {
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  return requireAssistantData(response.data, 'Unable to claim welcome gift')
}

export async function getAssistantWeeklyDiscount(): Promise<AssistantWeeklyDiscount | null> {
  const response = await api.get<
    AssistantAPIResponse<AssistantWeeklyDiscount | null>
  >('/api/assistant/weekly-discount', {
    disableDuplicate: true,
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  return requireAssistantData(response.data, 'Unable to load weekly discount')
}

export async function claimAssistantWeeklyDiscount(): Promise<{
  discount: AssistantWeeklyDiscount
  already_claimed: boolean
}> {
  const response = await api.post<
    AssistantAPIResponse<{
      discount: AssistantWeeklyDiscount
      already_claimed: boolean
    }>
  >('/api/assistant/weekly-discount/claim', undefined, {
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  return requireAssistantData(response.data, 'Unable to claim weekly discount')
}

export async function revealAssistantPrivateCard(id: string): Promise<string> {
  const response = await api.get<
    AssistantAPIResponse<{
      payload?: Record<string, string>
    }>
  >(`/api/assistant/cards/${encodeURIComponent(id)}/reveal`, {
    disableDuplicate: true,
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  const data = requireAssistantData(
    response.data,
    'Unable to retrieve the private credential'
  )
  const value = data.payload?.api_key?.trim()
  if (!value) throw new Error('Unable to retrieve the private credential')
  return value
}

export async function getAssistantConversationHistory(
  archived = false,
  ownerUserId?: number,
  limit?: number,
  cursor?: string
): Promise<AssistantConversationHistory> {
  const params: {
    archived?: true
    user_id?: number
    limit?: number
    cursor?: string
  } = {}
  if (archived) params.archived = true
  if (ownerUserId !== undefined) params.user_id = ownerUserId
  if (typeof limit === 'number' && Number.isSafeInteger(limit) && limit > 0) {
    params.limit = Math.min(limit, 100)
  }
  if (cursor?.trim()) params.cursor = cursor.trim()
  const response = await api.get<
    AssistantAPIResponse<AssistantConversationHistory>
  >('/api/assistant/conversations', {
    ...(Object.keys(params).length > 0 ? { params } : {}),
    disableDuplicate: true,
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  return requireAssistantData(
    response.data,
    'Unable to load conversation history'
  )
}

async function setAssistantConversationArchived(
  id: number,
  action: 'archive' | 'unarchive'
): Promise<AssistantConversationArchiveResult> {
  const response = await api.post<
    AssistantAPIResponse<AssistantConversationArchiveResult>
  >(
    `/api/assistant/conversations/${encodeURIComponent(id)}/${action}`,
    {},
    {
      disableDuplicate: true,
      skipBusinessError: true,
      skipErrorHandler: true,
    }
  )
  return requireAssistantData(
    response.data,
    action === 'archive'
      ? 'Unable to archive conversation'
      : 'Unable to restore conversation'
  )
}

export function archiveAssistantConversation(
  id: number
): Promise<AssistantConversationArchiveResult> {
  return setAssistantConversationArchived(id, 'archive')
}

export function unarchiveAssistantConversation(
  id: number
): Promise<AssistantConversationArchiveResult> {
  return setAssistantConversationArchived(id, 'unarchive')
}

export async function getAssistantConversationHistoryDetail(
  id: number
): Promise<AssistantConversationHistoryDetail> {
  const response = await api.get<
    AssistantAPIResponse<AssistantConversationHistoryDetail>
  >(`/api/assistant/conversations/${encodeURIComponent(id)}`, {
    disableDuplicate: true,
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  return requireAssistantData(
    response.data,
    'Unable to load conversation details'
  )
}

export async function getAssistantHandoff(): Promise<AssistantHandoff | null> {
  const response = await api.get<AssistantAPIResponse<AssistantHandoff | null>>(
    '/api/assistant/handoffs/self',
    {
      disableDuplicate: true,
      skipBusinessError: true,
      skipErrorHandler: true,
    }
  )
  return requireAssistantData(response.data, 'Unable to load support request')
}

export async function submitAssistantHandoff(
  message: string,
  confirmationToken?: string
): Promise<AssistantHandoff> {
  const response = await api.post<AssistantAPIResponse<AssistantHandoff>>(
    '/api/assistant/handoffs',
    {
      confirmed: true,
      message,
      ...(confirmationToken ? { confirmation_token: confirmationToken } : {}),
    },
    { skipBusinessError: true, skipErrorHandler: true }
  )
  return requireAssistantData(response.data, 'Unable to contact support')
}

export async function listAssistantHandoffs(
  status: 'pending' | 'resolved' = 'pending'
): Promise<AssistantHandoff[]> {
  const response = await api.get<AssistantAPIResponse<AssistantHandoff[]>>(
    '/api/assistant/admin/handoffs',
    {
      params: { status },
      skipBusinessError: true,
      skipErrorHandler: true,
    }
  )
  return requireAssistantData(response.data, 'Unable to load support requests')
}

export async function getAssistantIntentSummary(
  days = 30
): Promise<AssistantIntentSummary[]> {
  const response = await api.get<
    AssistantAPIResponse<AssistantIntentSummary[]>
  >('/api/assistant/admin/intents', {
    params: { days },
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  return requireAssistantData(response.data, 'Unable to load intent summary')
}

export async function getAssistantProfileSummary(
  days = 30
): Promise<AssistantProfileSummary[]> {
  const response = await api.get<
    AssistantAPIResponse<AssistantProfileSummary[]>
  >('/api/assistant/admin/profiles', {
    params: { days },
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  return requireAssistantData(response.data, 'Unable to load profile summary')
}

export async function getAssistantFirstQuestionSummary(
  days = 30
): Promise<AssistantFirstQuestionSummary[]> {
  const response = await api.get<
    AssistantAPIResponse<AssistantFirstQuestionSummary[]>
  >('/api/assistant/admin/first-questions', {
    params: { days },
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  return requireAssistantData(
    response.data,
    'Unable to load first-question summary'
  )
}

export async function getAssistantFundingSummary(
  days = 30
): Promise<AssistantFundingSummary> {
  const response = await api.get<AssistantAPIResponse<AssistantFundingSummary>>(
    '/api/assistant/admin/funding',
    {
      params: { days },
      skipBusinessError: true,
      skipErrorHandler: true,
    }
  )
  return requireAssistantData(response.data, 'Unable to load assistant funding')
}

export async function resolveAssistantHandoff(
  id: number,
  note: string
): Promise<AssistantHandoff> {
  const response = await api.post<AssistantAPIResponse<AssistantHandoff>>(
    `/api/assistant/admin/handoffs/${id}/resolve`,
    { note },
    { skipBusinessError: true, skipErrorHandler: true }
  )
  return requireAssistantData(
    response.data,
    'Unable to resolve support request'
  )
}
