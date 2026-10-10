/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { api } from '@/lib/api'

import type { MarketAIReviewMode } from './mode-fields'

export interface MarketAIReviewSettings {
  tool_mode: MarketAIReviewMode
  store_mode: MarketAIReviewMode
  review_group: string
  review_model: string
  engine: 'openai_moderation'
  supported_inputs: string[]
  categories: string[]
}
export interface MarketAIReviewRecord {
  id: number
  source: string
  target_id: string
  content_version: string
  content_hash: string
  mode: MarketAIReviewMode
  status: 'pending' | 'running' | 'completed' | 'failed' | 'cancelled'
  attempts: number
  review_model: string
  response_model: string
  flagged: boolean | null
  categories: string[] | null
  recommendation: 'approve' | 'reject' | null
  outcome:
    | 'reference'
    | 'approved'
    | 'rejected'
    | 'manual_required'
    | 'stale'
    | 'overridden'
    | null
  applied: boolean
  error_code: string | null
  created_at: number
  updated_at: number
  completed_at: number
  checked_at: number
  coverage: 'public_listing_text'
}
type Envelope<T> = { success: boolean; message?: string; data: T }
async function unwrap<T>(request: Promise<{ data: Envelope<T> }>): Promise<T> {
  const response = await request
  if (response.data.success !== true) {
    throw new Error(response.data.message || 'Failed to load AI review')
  }
  return response.data.data
}
const options = { skipErrorHandler: true, skipBusinessError: true }
const settingsPath = '/api/security/market-ai-review/settings'
export const marketAIReviewAPI = {
  settings: () =>
    unwrap<MarketAIReviewSettings>(api.get(settingsPath, options)),
  saveSettings: (
    modes: Pick<MarketAIReviewSettings, 'tool_mode' | 'store_mode'>
  ) => unwrap<MarketAIReviewSettings>(api.put(settingsPath, modes, options)),
  records: (source: 'tool' | 'product', id: string, versionId?: string) =>
    unwrap<{ rows: MarketAIReviewRecord[] }>(
      api.get(
        source === 'tool'
          ? `/api/tool-market/services/${encodeURIComponent(id)}/ai-reviews`
          : `/api/store/products/${encodeURIComponent(id)}/ai-reviews`,
        {
          ...options,
          params:
            source === 'tool' && versionId
              ? { version_id: versionId }
              : undefined,
        }
      )
    ),
}
