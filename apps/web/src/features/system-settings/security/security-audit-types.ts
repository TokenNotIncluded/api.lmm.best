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

export type ModerationQueueStats = {
  pending: number
  running: number
  completed: number
  failed: number
  cancelled: number
  flagged: number
  fined: number
  charged_quota: number
}

/** Only association metadata is rendered; appeal reasons and administrator notes stay private. */
export type ModerationAppeal = {
  id: number
  record_id: number
  status: 'pending' | 'approved' | 'rejected'
}

export type ModerationReview = {
  id: number
  created_at: number
  user_id: number
  group: string
  source_kind: 'openai_moderation'
  source: 'relay_input' | 'assistant_input' | 'assistant_output'
  mode: 'off' | 'tolerant' | 'strict'
  status: 'pending' | 'running' | 'completed' | 'failed' | 'cancelled'
  flagged: boolean
  categories: string[]
  review_model: string
  request_id: string
  fee_status: string
  fee_category: string
  requested_quota: number
  charged_quota: number
  fee_record_id: number
  input_truncated: boolean
  attempts: number
  completed_at: number
  subject_identifier?: string
  provider_calls?: Array<{
    attempt: number
    batch_index: number
    response_id: string
    request_id: string
  }>
  error?: string
}

export type ModerationReviewFilters = {
  page: number
  page_size: number
  group?: string
  status?: string
  source?: string
}

export type ModerationReviewPage = {
  rows: ModerationReview[]
  total: number
  page: number
  page_size: number
}
