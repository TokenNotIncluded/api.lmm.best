/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { getUserLogs } from '@/features/usage-logs/api'
import {
  usageLogSchema,
  type UsageLog,
} from '@/features/usage-logs/data/schema'
import { parseLogOther } from '@/features/usage-logs/lib/format'

export async function loadLatestRequest(): Promise<UsageLog | null> {
  const results = await Promise.all([
    getUserLogs({ type: 2, page_size: 1 }),
    getUserLogs({ type: 5, page_size: 1 }),
  ])
  const records: UsageLog[] = []
  for (const result of results) {
    if (!result.success || !result.data || !Array.isArray(result.data.items))
      {throw new Error('Latest request unavailable')}
    for (const item of result.data.items) {
      const parsed = usageLogSchema.safeParse(item)
      if (!parsed.success) throw new Error('Invalid request record')
      records.push(parsed.data)
    }
  }
  return (
    records.sort((a, b) => b.created_at - a.created_at || b.id - a.id)[0] ??
    null
  )
}
export function latestRequestStatus(
  log: UsageLog
): 'failed' | 'successful' | 'recorded' {
  const other = parseLogOther(log.other)
  if (
    log.type === 5 ||
    other?.stream_status?.status === 'error' ||
    other?.stream_status?.end_error
  )
    {return 'failed'}
  try {
    if (JSON.parse(log.other || '{}').acquisition_success_v1 === true)
      {return 'successful'}
  } catch {
    /* Legacy records may not include success evidence. */
  }
  return 'recorded'
}
