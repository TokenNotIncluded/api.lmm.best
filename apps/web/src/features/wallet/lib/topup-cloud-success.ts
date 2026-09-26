/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import type { TopupRecord } from '../types'

export type PendingTopupCloud = {
  userId: number
  launchedAt: number
  expiresAt: number
  beforeQuota: number
  expectedCredit: number
  // Optional only to safely discard intents from older clients, never to guess.
  baselineSuccessId?: number
  attemptId?: string
  tradeNo?: string
}

/** An unrelated success, even of the same amount, must never confirm checkout. */
export function findConfirmedTopup(
  records: TopupRecord[],
  pending: PendingTopupCloud
) {
  if (!pending.tradeNo) return undefined
  return records.find(
    (record) =>
      record.status === 'success' &&
      record.user_id === pending.userId &&
      record.trade_no === pending.tradeNo
  )
}
