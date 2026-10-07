/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { api } from '@/lib/api'

import { MarketAPIError, type MarketOAuthClient } from './api'
import { collectMarketPages } from './connection-utils'

export type MetaDelegation = {
  enabled: boolean
  max_total_quota: number
  expires_at: number
  updated_at: number
}
export type MetaDelegationTarget = {
  kind: 'personal' | 'oauth'
  id: string
}
export type MetaDelegationInput = Pick<
  MetaDelegation,
  'enabled' | 'max_total_quota' | 'expires_at'
>

type Envelope<T> = {
  success: boolean
  data: T
  code?: string
}

async function unwrap<T>(request: Promise<{ data: Envelope<T> }>): Promise<T> {
  try {
    const { data } = await request
    if (!data.success) {
      throw new MarketAPIError(data.code || 'TOOL_MARKET_UNAVAILABLE')
    }
    return data.data
  } catch (error) {
    if (error instanceof MarketAPIError) throw error
    if (error && typeof error === 'object' && 'response' in error) {
      const code = (error.response as { data?: { code?: unknown } } | undefined)
        ?.data?.code
      if (typeof code === 'string' && /^TOOL_MARKET_[A-Z_]{1,64}$/.test(code)) {
        throw new MarketAPIError(code)
      }
    }
    throw new MarketAPIError('TOOL_MARKET_UNAVAILABLE')
  }
}

function path(target: MetaDelegationTarget): string {
  if (
    !['personal', 'oauth'].includes(target.kind) ||
    !target.id ||
    target.id.length > 128 ||
    Array.from(target.id).some((character) => {
      const code = character.charCodeAt(0)
      return code < 32 || code === 127
    })
  ) {
    throw new MarketAPIError('TOOL_MARKET_INVALID_INPUT')
  }
  return `/api/tool-market/meta-delegations/${target.kind}/${encodeURIComponent(target.id)}`
}

export function metaDelegationQuota(raw: string): number | undefined {
  if (!/^(0|[1-9]\d*)$/.test(raw) || raw.length > 16) return undefined
  const quota = Number(raw)
  return Number.isSafeInteger(quota) && quota >= 0 ? quota : undefined
}

export const metaDelegationAPI = {
  oauthClients: (signal?: AbortSignal) =>
    collectMarketPages<MarketOAuthClient>((offset, limit) =>
      unwrap<MarketOAuthClient[]>(
        api.get('/api/tool-market/meta-delegations/oauth-clients', {
          params: { offset, limit },
          signal,
        })
      )
    ),
  get: (target: MetaDelegationTarget) =>
    unwrap<MetaDelegation>(api.get(path(target))),
  set: (target: MetaDelegationTarget, input: MetaDelegationInput) => {
    if (
      typeof input.enabled !== 'boolean' ||
      !Number.isSafeInteger(input.max_total_quota) ||
      input.max_total_quota < 0 ||
      !Number.isSafeInteger(input.expires_at) ||
      input.expires_at < 0 ||
      (!input.enabled &&
        (input.max_total_quota !== 0 || input.expires_at !== 0))
    ) {
      throw new MarketAPIError('TOOL_MARKET_INVALID_INPUT')
    }
    return unwrap<MetaDelegation>(api.put(path(target), input))
  },
}
