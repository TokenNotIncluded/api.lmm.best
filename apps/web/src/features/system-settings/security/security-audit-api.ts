/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
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
/*
Copyright (C) 2026 LIghtJUNction
*/
import { api } from '@/lib/api'

import type {
  AdminSecurityPolicy,
  SecurityAuditEnvelope,
  SecurityAuditAIReviewPage,
  SecurityAuditFilters,
  SecurityAuditPage,
  SecurityAuditStats,
} from './security-audit-types'

export const ADMIN_SECURITY_POLICY_ENDPOINT = '/api/security/admin/policy'
export const ADMIN_SECURITY_STATS_ENDPOINT = '/api/security/admin/stats'
export const ADMIN_SECURITY_EVENTS_ENDPOINT = '/api/security/admin/events'
export const ADMIN_SECURITY_AI_REVIEWS_ENDPOINT =
  '/api/security/admin/ai-reviews'
export const ADMIN_ASSISTANT_REVIEW_RUNS_ENDPOINT =
  '/api/security/admin/review-runs'
export const ADMIN_ASSISTANT_REVIEW_CLEANUP_PREVIEW_ENDPOINT = `${ADMIN_ASSISTANT_REVIEW_RUNS_ENDPOINT}/cleanup-preview`

export type AssistantReviewRunCleanupData = {
  task_type: 'assistant_review'
  keep: number
  eligible_count: number
  deleted_count: number
}

export type AssistantReviewRunCleanupResponse =
  SecurityAuditEnvelope<AssistantReviewRunCleanupData>

export async function getAdminSecurityPolicy() {
  const response = await api.get<SecurityAuditEnvelope<AdminSecurityPolicy>>(
    ADMIN_SECURITY_POLICY_ENDPOINT,
    { skipBusinessError: true, skipErrorHandler: true }
  )
  return response.data
}

export async function getAdminSecurityStats(
  filters: Omit<SecurityAuditFilters, 'page' | 'page_size'> = {}
) {
  const response = await api.get<SecurityAuditEnvelope<SecurityAuditStats>>(
    ADMIN_SECURITY_STATS_ENDPOINT,
    {
      params: compactFilters(filters),
      skipBusinessError: true,
      skipErrorHandler: true,
    }
  )
  return response.data
}

export async function listAdminSecurityEvents(filters: SecurityAuditFilters) {
  const response = await api.get<SecurityAuditEnvelope<SecurityAuditPage>>(
    ADMIN_SECURITY_EVENTS_ENDPOINT,
    {
      params: compactFilters(filters),
      skipBusinessError: true,
      skipErrorHandler: true,
    }
  )
  return response.data
}

export async function listAdminSecurityAIReviews(
  filters: SecurityAuditFilters
) {
  const response = await api.get<
    SecurityAuditEnvelope<SecurityAuditAIReviewPage>
  >(ADMIN_SECURITY_AI_REVIEWS_ENDPOINT, {
    params: compactFilters(filters),
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  return response.data
}

export async function previewAssistantReviewRunCleanup(
  keep: number
): Promise<AssistantReviewRunCleanupResponse> {
  const response = await api.get<AssistantReviewRunCleanupResponse>(
    ADMIN_ASSISTANT_REVIEW_CLEANUP_PREVIEW_ENDPOINT,
    { params: { keep }, skipErrorHandler: true }
  )
  return response.data
}

export async function deleteAssistantReviewRuns(
  keep: number,
  expectedCount: number,
  proofToken?: string
): Promise<AssistantReviewRunCleanupResponse> {
  const response = await api.delete<AssistantReviewRunCleanupResponse>(
    ADMIN_ASSISTANT_REVIEW_RUNS_ENDPOINT,
    {
      params: { keep, expected_count: expectedCount },
      headers: proofToken ? { 'X-Security-Proof': proofToken } : undefined,
      skipErrorHandler: true,
    }
  )
  return response.data
}

function compactFilters(
  filters: Partial<SecurityAuditFilters>
): Record<string, string | number> {
  const params: Record<string, string | number> = {}
  if (filters.page) params.p = filters.page
  if (filters.page_size) params.page_size = filters.page_size
  if (filters.category) params.category = filters.category
  if (filters.group) params.group = filters.group
  if (filters.decision) params.decision = filters.decision
  if (filters.source) params.source = filters.source
  return params
}
