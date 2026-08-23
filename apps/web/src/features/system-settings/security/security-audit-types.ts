/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

export type SecurityAuditEnvelope<T> = {
  success: boolean
  message?: string
  data?: T
}

export type AdminSecurityRule = {
  id: string
  name: string
  category: string
  layer: string
  severity: string
  source: string
  version: string
  description: string
  enabled: boolean
  groups: string[]
}

export type AdminSecurityPolicy = {
  settings: {
    enabled: boolean
    on_prompt: boolean
    action: 'block' | 'audit' | string
  }
  rules: AdminSecurityRule[]
}

export type SecurityAuditStats = {
  start_timestamp: number
  end_timestamp: number
  total_matches: number
  blocked_matches: number
  audited_matches: number
  affected_requests: number
  affected_users: number
  by_category?: Array<{ key: string; count: number }>
  by_rule?: Array<{ key: string; count: number }>
  ai_review?: {
    total: number
    completed: number
    violations: number
    abuses: number
    failed: number
    by_group?: Array<{ key: string; count: number }>
  }
}

/**
 * The audit API deliberately returns metadata and digests only. Keep preview
 * and secret-shaped fields out of this UI type so a future backend addition
 * cannot accidentally become visible here.
 */
export type SecurityAuditEvent = {
  id: number
  created_at: number
  request_id?: string
  user_id?: number
  username?: string
  token_id?: number
  channel_id?: number
  model_name?: string
  group?: string
  endpoint?: string
  decision?: string
  rule_id?: string
  rule_name?: string
  category?: string
  layer?: string
  severity?: string
  source?: string
  rule_version?: string
  match_count?: number
  review_model?: string
  status?: string
  violation?: boolean
  abuse?: boolean
  rules?: string[]
  explanation?: string
}

export type SecurityAuditAIReview = {
  id: number
  created_at: number
  request_id?: string
  user_id?: number
  group?: string
  review_model?: string
  intensity?: string
  status?: string
  violation?: boolean
  abuse?: boolean
  rules?: string[]
  explanation?: string
}

export type SecurityAuditPage = {
  items: SecurityAuditEvent[]
  total: number
  page: number
  page_size: number
}

export type SecurityAuditFilters = {
  page: number
  page_size: number
  category?: string
  group?: string
  decision?: string
  source?: string
}

export type AssistantReviewResult = {
  window_start: number
  window_end: number
  observed_at: number
  intents?: Array<{ intent: string; count: number }>
  distilled_intents?: Array<{ intent: string; count: number }>
  profiles?: Array<{ profile: string; count: number }>
  presets?: Array<{
    preset_id: string
    clicks: number
    conversations: number
    recommendations: number
    approvals: number
  }>
  first_questions?: Array<{
    question: string
    count: number
    last_asked_at?: number
  }>
  current_pending_support?: number
  current_open_security_incidents?: number
  commerce?: {
    chat_users: number
    successful_topup_orders: number
    successful_subscription_orders: number
    paid_users: number
    conversion_rate_percent: number
    refund_count: number
    refund_amount_micros: number
  }
  security?: {
    total_matches: number
    blocked_matches: number
    audited_matches: number
    affected_requests: number
    affected_users: number
    error_log_count: number
  }
  actions?: Array<{ code: string; count: number }>
}

export type AssistantReviewTask = {
  task_id: string
  status: 'pending' | 'running' | 'succeeded' | 'failed' | string
  result?: AssistantReviewResult | null
  error?: string
  created_at: number
  updated_at: number
}

export type SecurityAuditAIReviewPage = {
  items: SecurityAuditAIReview[]
  total: number
  page: number
  page_size: number
  available?: boolean
}
