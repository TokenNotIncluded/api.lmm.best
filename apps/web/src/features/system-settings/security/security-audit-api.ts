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
  SecurityAuditEnvelope,
  ModerationAppeal,
} from './security-audit-types'

export type ModerationModelCatalog = {
  group: string
  models: string[]
}

export async function getModerationModels(group: string) {
  const response = await api.get<SecurityAuditEnvelope<ModerationModelCatalog>>(
    '/api/security/admin/moderation/models',
    { params: { group }, skipBusinessError: true, skipErrorHandler: true }
  )
  if (!response.data.success) {
    throw new Error(response.data.message || 'Unable to load moderation models')
  }
  return response.data.data?.models ?? []
}

export async function listModerationReviews(
  filters: import('./security-audit-types').ModerationReviewFilters
) {
  const response = await api.get<
    SecurityAuditEnvelope<import('./security-audit-types').ModerationReviewPage>
  >('/api/security/admin/moderation-reviews', {
    params: {
      p: filters.page,
      page_size: filters.page_size,
      ...(filters.group ? { group: filters.group } : {}),
      ...(filters.status ? { status: filters.status } : {}),
      ...(filters.source ? { source: filters.source } : {}),
    },
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  return response.data
}

export async function getModerationStats() {
  const response = await api.get<
    SecurityAuditEnvelope<import('./security-audit-types').ModerationQueueStats>
  >('/api/security/admin/moderation-stats', {
    skipBusinessError: true,
    skipErrorHandler: true,
  })
  return response.data
}

export async function listModerationAppeals() {
  const response = await api.get<SecurityAuditEnvelope<ModerationAppeal[]>>(
    '/api/security/admin/violation-fee-appeals',
    { skipBusinessError: true, skipErrorHandler: true }
  )
  return response.data
}
