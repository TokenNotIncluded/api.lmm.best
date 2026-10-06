/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import { api } from '@/lib/api'

import {
  collectMarketPages,
  connectionTokenInput,
  type ConnectionPermissions,
} from './connection-utils'
import { marketDraftBody } from './draft-body'
import { marketInvokeBody } from './invoke-body'

export type MarketConfig = {
  enabled: boolean
  fee_bps: number
  recipient_id: number
  quota_per_unit: number
  web_client_id: string
  mcp_path: string
  builtin_enabled?: boolean
}
export type MarketService = {
  id: string
  name?: string
  owner_id: number
  live_version_id: string
  draft_version_id: string
  status: string
  created_at: number
}
export type MarketSummary = {
  id: string
  owner_id: number
  version_id: string
  name: string
  description: string
  execution_type: string
  tool_count?: number
  pricing?: string
  min_price_quota?: number
  max_price_quota?: number
  metered_tools?: number
}
export type BillingRule = {
  metric: string
  rate_quota: number
  max_quantity: number
}
export type MarketTool = {
  tool_id: string
  version_id: string
  name: string
  description: string
  input_schema: string
  output_schema: string
  permissions: string
  price_quota: number

  billing_mode?: 'input_tokens' | 'metered' | ''
  billing_rules?: BillingRule[]
  available_metering_metrics?: string[]
  input_token_price_quota?: number
  max_input_tokens?: number
}
export type MarketDetail = {
  service: MarketService
  version: {
    id: string
    name: string
    description: string
    endpoint: string
    execution_type: string
    visibility: string
    status: string
    review_note: string
  }
  tools: MarketTool[]
  pricing: string
  validated: boolean
  allowed_users?: number[]
}
export type ToolInput = {
  name: string
  description: string
  input_schema: Record<string, unknown> | string
  output_schema?: Record<string, unknown> | string
  input_schema_json?: string
  output_schema_json?: string
  permissions: string[]
  price_quota: number

  billing_mode?: 'input_tokens' | 'metered' | ''
  billing_rules?: BillingRule[]
  available_metering_metrics?: string[]
  input_token_price_quota?: number
  max_input_tokens?: number
}
export type DraftInput = {
  name: string
  description: string
  endpoint: string
  execution_type: 'remote'
  visibility: string
  allowed_users: number[]
  tools: ToolInput[]
}
export type Installation = {
  client_id: string
  tool_id: string
  version_id: string
}
export type Grant = Installation & {
  id: string
  max_price_quota: number
  max_total_quota: number
  max_calls: number
  reserved_quota: number
  spent_quota: number
  successful_calls: number
  reserved_calls: number
  expires_at: number
  revoked_at: number
}
export type MarketToken = {
  id: string
  client_id: string
  can_invoke: boolean
  can_manage: boolean
  expires_at: number
  revoked_at: number
}
export type MarketCall = {
  usage_source?: 'tool_reported' | 'platform_verified'
  usage_quantities?: Record<string, number>
  id: string
  service_id: string
  tool_id: string
  version_id: string
  client_id: string
  execution_status: string
  settlement_status: string
  price_quota: number
  created_at: number
  resolve_by: number
}
export type MarketReport = {
  call_id: string
  user_id: number
  service_id: string
  owner_id: number
  reason: string
  evidence: string
  status: 'pending' | 'confirmed' | 'dismissed'
  review_note: string
  reviewed_by: number
  created_at: number
  reviewed_at: number
}
export type CallResponse = {
  call: MarketCall
  result?: unknown
  result_expired: boolean
  error_code?: string
}
export type MarketAuthentication = {
  mode: 'none' | 'bearer' | 'api_key'
  secret?: string
}
export type MarketCredentials = {
  mode: MarketAuthentication['mode']
  configured: boolean
  updated_at: number
}
export type MarketInputResponse = {
  confirmation: {
    action: 'accept' | 'decline' | 'cancel'
    content?: { confirmed: true }
  }
}
export type Income = {
  id: string
  call_id: string
  kind: string
  quota: number
  fee_bps: number
  created_at: number
}
export type Budget = {
  scope: string
  scope_id: string
  limit_quota: number
  spent_quota: number
  reserved_quota: number
}
export type ClientDisconnect = {
  client_id: string
  tokens_revoked: number
  grants_revoked: number
  tools_unloaded: number
}
export type ClientRemoval = {
  client_id: string
  tokens_hidden: number
  grants_hidden: number
  tools_unloaded: number
}
export class MarketAPIError extends Error {
  readonly code: string

  constructor(code: string) {
    super(code)
    this.code = code
    this.name = 'MarketAPIError'
  }
}
type Envelope<T> = {
  success: boolean
  data: T
  code?: string
  message?: string
}

// Eligible authorization targets only; this does not report live token status.
export type MarketOAuthClient = { client_id: string }

const base = '/api/tool-market'
async function unwrap<T>(request: Promise<{ data: Envelope<T> }>): Promise<T> {
  try {
    const { data } = await request
    if (!data.success) {
      throw new MarketAPIError(data.code || 'TOOL_MARKET_UNAVAILABLE')
    }
    return data.data
  } catch (error) {
    if (error instanceof MarketAPIError) throw error
    // Preserve a bounded API error code, never server exception text or URLs.
    if (error && typeof error === 'object' && 'response' in error) {
      const response = error.response as
        | { data?: { code?: unknown } }
        | undefined
      const code = response?.data?.code
      if (typeof code === 'string' && /^TOOL_MARKET_[A-Z_]{1,64}$/.test(code)) {
        throw new MarketAPIError(code)
      }
    }
    throw error
  }
}
export const marketAPI = {
  config: () => unwrap<MarketConfig>(api.get(`${base}/config`)),
  list: (q: string, offset = 0, executionType = '') =>
    unwrap<MarketSummary[]>(
      api.get(base, {
        params: { q, offset, execution_type: executionType, limit: 30 },
      })
    ),
  detail: (
    id: string,
    mode: 'published' | 'draft' | 'review' = 'published',
    signal?: AbortSignal
  ) =>
    unwrap<MarketDetail>(
      api.get(
        `${base}/services/${id}${mode === 'published' ? '' : `/${mode}`}`,
        {
          signal,
        }
      )
    ),
  mine: <T>(kind: string, signal?: AbortSignal) =>
    collectMarketPages<T>((offset, limit) =>
      unwrap<T[]>(
        api.get(`${base}/mine/${encodeURIComponent(kind)}`, {
          params: { offset, limit },
          signal,
        })
      )
    ),
  inspect: (
    endpoint: string,
    authentication?: MarketAuthentication,
    reference?: { service_id: string; version_id: string }
  ) =>
    unwrap<ToolInput[]>(
      api.post(`${base}/inspect`, { endpoint, authentication, ...reference })
    ),
  credentials: (id: string, versionID: string) =>
    unwrap<MarketCredentials>(
      api.get(`${base}/services/${id}/credentials`, {
        params: { version_id: versionID },
      })
    ),
  setCredentials: (
    id: string,
    input: MarketAuthentication & {
      version_id: string
      copy_from_version_id?: string
    }
  ) =>
    unwrap<MarketCredentials>(
      api.put(`${base}/services/${id}/credentials`, input)
    ),
  save: (id: string | undefined, input: DraftInput) =>
    unwrap<MarketService>(
      id
        ? api.put(`${base}/services/${id}/draft`, marketDraftBody(input), {
            headers: { 'Content-Type': 'application/json' },
          })
        : api.post(`${base}/services`, marketDraftBody(input), {
            headers: { 'Content-Type': 'application/json' },
          })
    ),
  validate: (id: string) =>
    unwrap<null>(api.post(`${base}/services/${id}/validate`)),
  submit: (id: string, version_id: string) =>
    unwrap<null>(api.post(`${base}/services/${id}/submit`, { version_id })),
  activate: (id: string, version_id: string) =>
    unwrap<null>(api.post(`${base}/services/${id}/activate`, { version_id })),
  reviews: () => unwrap<MarketService[]>(api.get(`${base}/reviews`)),
  deleteService: (id: string) =>
    unwrap<null>(api.delete(`${base}/services/${id}`)),
  review: (id: string, version_id: string, approve: boolean, note: string) =>
    unwrap<null>(
      api.post(`${base}/services/${id}/review`, { version_id, approve, note })
    ),
  favorite: (id: string, favorite: boolean) =>
    unwrap<null>(api.put(`${base}/services/${id}/favorite`, { favorite })),
  install: (input: Installation, loaded: boolean) =>
    unwrap<null>(api.put(`${base}/installations`, { ...input, loaded })),
  grant: (
    input: Installation & {
      max_price_quota: number
      max_total_quota: number
      max_calls: number
      expires_at: number
    }
  ) => unwrap<Grant>(api.post(`${base}/grants`, input)),
  revokeGrant: (id: string) => unwrap<null>(api.delete(`${base}/grants/${id}`)),
  removeGrantRecord: (id: string) =>
    unwrap<null>(api.delete(`${base}/grants/${id}/record`)),
  invoke: (input: {
    tool_id: string
    version_id: string
    grant_id: string
    request_id: string
    arguments: unknown
    arguments_json?: string
    request_state?: string
    input_responses?: MarketInputResponse
  }) =>
    unwrap<CallResponse>(
      api.post(`${base}/invoke`, marketInvokeBody(input), {
        timeout: 60000,
        headers: { 'Content-Type': 'application/json' },
      })
    ),
  result: (id: string) =>
    unwrap<CallResponse>(api.get(`${base}/calls/${id}/result`)),
  report: (id: string, reason: string) =>
    unwrap<MarketReport>(api.post(`${base}/calls/${id}/report`, { reason })),
  reports: (offset = 0) =>
    unwrap<MarketReport[]>(
      api.get(`${base}/reports`, { params: { offset, limit: 30 } })
    ),
  reviewReport: (id: string, confirmed: boolean, note: string) =>
    unwrap<null>(api.post(`${base}/reports/${id}/review`, { confirmed, note })),
  calls: (offset = 0) =>
    unwrap<MarketCall[]>(
      api.get(`${base}/calls`, { params: { offset, limit: 30 } })
    ),
  income: (offset = 0) =>
    unwrap<Income[]>(
      api.get(`${base}/income`, { params: { offset, limit: 30 } })
    ),
  token: (clientID: string, permissions?: ConnectionPermissions) =>
    unwrap<{ token: string; record: MarketToken }>(
      api.post(`${base}/tokens`, connectionTokenInput(clientID, permissions))
    ),
  revokeToken: (id: string) => unwrap<null>(api.delete(`${base}/tokens/${id}`)),
  removeTokenRecord: (id: string) =>
    unwrap<null>(api.delete(`${base}/tokens/${id}/record`)),
  disconnectClient: (clientID: string) =>
    unwrap<ClientDisconnect>(
      api.post(`${base}/clients/disconnect`, { client_id: clientID })
    ),
  removeClient: (clientID: string) =>
    unwrap<ClientRemoval>(
      api.post(`${base}/clients/remove`, { client_id: clientID })
    ),
  budget: (input: { scope: string; scope_id: string; limit_quota: number }) =>
    unwrap<null>(api.put(`${base}/budgets`, input)),
  configure: (
    input: Pick<MarketConfig, 'enabled' | 'fee_bps' | 'recipient_id'>
  ) => unwrap<null>(api.put(`${base}/config`, input)),
  pause: (id: string, paused: boolean) =>
    unwrap<null>(api.put(`${base}/services/${id}/paused`, { paused })),
}

export { marketQuota } from './money'
