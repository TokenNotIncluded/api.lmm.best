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
  baselineSuccessId: number
  beforeQuota: number
  expectedCredit: number
}

/** Only a newly completed server order may start the credited-token animation. */
export function findConfirmedTopup(
  records: TopupRecord[],
  pending: PendingTopupCloud
) {
  const earliestCompletion = Math.floor(pending.launchedAt / 1000) - 5
  return records.find(
    (record) =>
      record.status === 'success' &&
      record.id > pending.baselineSuccessId &&
      (pending.baselineSuccessId > 0 ||
        (record.create_time >= earliestCompletion &&
          (record.complete_time ?? record.create_time) >= earliestCompletion &&
          (pending.expectedCredit <= 0 ||
            Math.abs(record.amount - pending.expectedCredit) <=
              Math.max(1, pending.expectedCredit * 0.01))))
  )
}
