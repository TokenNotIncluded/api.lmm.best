/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import type { ApiKeyFormData } from '@/features/keys/types'
import { parseQuotaFromDollars } from '@/lib/format'

export function testKeyQuota(input: string): number | null {
  if (!input.trim()) return null
  const amount = Number(input)
  if (!Number.isFinite(amount) || amount <= 0) return null
  const quota = parseQuotaFromDollars(amount)
  return Number.isSafeInteger(quota) && quota > 0 ? quota : null
}

export function testKeyPayload(
  quota: number,
  group: string,
  confirmations: number
): ApiKeyFormData {
  if (!Number.isSafeInteger(quota) || quota <= 0 || !group.trim()) {
    throw new Error('Invalid test key settings')
  }
  return {
    name: `test-${new Date().toISOString().slice(0, 10)}-${crypto.randomUUID().slice(0, 8)}`,
    // Reveal once is an existing storage policy, not a request-count limit.
    one_time_reveal: true,
    remain_quota: quota,
    unlimited_quota: false,
    expired_time: -1,
    model_limits_enabled: false,
    model_limits: '',
    allow_ips: '',
    group,
    auto_groups: [],
    cross_group_retry: false,
    group_warning_confirmations: confirmations,
  }
}
