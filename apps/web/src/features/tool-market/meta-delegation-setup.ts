/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import {
  metaDelegationAPI,
  metaDelegationQuota,
  type MetaDelegation,
  type MetaDelegationTarget,
} from './meta-delegation-api'

export type MetaDelegationSetup = { enabled: boolean; quota: string }
export const defaultMetaDelegationSetup: MetaDelegationSetup = {
  enabled: true,
  quota: '0',
}

// Called only after a new token was explicitly issued with invoke/manage.
// A failed delegation must remain visible; it must not be reported as enabled.
export async function configureIssuedMetaDelegation(
  target: MetaDelegationTarget,
  value: MetaDelegationSetup,
  permitted: boolean,
  expiresAt: number
): Promise<MetaDelegation | undefined> {
  if (!value.enabled) return undefined
  if (!permitted) throw new Error('TOOL_MARKET_DENIED')
  const quota = metaDelegationQuota(value.quota)
  if (quota === undefined) throw new Error('TOOL_MARKET_INVALID_INPUT')
  return metaDelegationAPI.set(target, {
    enabled: true,
    max_total_quota: quota,
    expires_at: expiresAt,
  })
}
