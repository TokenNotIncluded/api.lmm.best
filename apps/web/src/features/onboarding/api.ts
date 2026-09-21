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
import { api } from '@/lib/api'

export type DeveloperAccessRequest = {
  id: number
  status: 'pending' | 'approved' | 'rejected'
  reason: string
  source:
    | 'assistant_recommendation'
    | 'user_edited'
    | 'assistant_request'
    | 'assistant_direct_grant'
    | 'legacy'
  ai_recommendation: string
  admin_note: string
  created_at: number
  reviewed_at: number
}

export function developerAccessRequestQueryKey(userId: number) {
  return ['assistant-developer-access-request', userId] as const
}

type ApiEnvelope<T> = {
  success: boolean
  code?: string
  message?: string
  data: T
}

async function unwrap<T>(request: Promise<{ data: ApiEnvelope<T> }>) {
  const response = await request
  if (!response.data.success) {
    const error = new Error(
      response.data.message || 'Developer access request failed'
    )
    Object.assign(error, { code: response.data.code })
    throw error
  }
  return response.data.data
}

export function getDeveloperAccessRequest() {
  return unwrap<DeveloperAccessRequest | null>(
    api.get('/api/user/developer-access/request', {
      skipBusinessError: true,
      skipErrorHandler: true,
    })
  )
}

export function submitDeveloperAccessRequest(input: {
  reason: string
  ai_recommendation?: string
  confirmation_token?: string
  confirmed: true
}) {
  return unwrap<DeveloperAccessRequest>(
    api.post('/api/user/developer-access/request', input, {
      skipBusinessError: true,
      skipErrorHandler: true,
    })
  )
}
