/*
Copyright (C) 2026 LIghtJUNction
*/
import { toIntlLocale } from '@/i18n/languages'

import type { ModerationAppeal, ModerationReview } from './security-audit-types'

export function recordedCount(value: unknown, locale?: string) {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
    ? value.toLocaleString(toIntlLocale(locale))
    : undefined
}

export function recordedCredits(value: unknown, locale?: string) {
  const count = recordedCount(value, locale)
  return count === undefined ? undefined : `${count} CREDIT`
}

export function summarizePageAppeals(
  reviews: ModerationReview[],
  appeals: ModerationAppeal[]
) {
  const recordIds = new Set(
    reviews.map((review) => review.fee_record_id).filter((id) => id > 0)
  )
  const linked = appeals.filter((appeal) => recordIds.has(appeal.record_id))
  return {
    pending: linked.filter((appeal) => appeal.status === 'pending').length,
    approved: linked.filter((appeal) => appeal.status === 'approved').length,
    rejected: linked.filter((appeal) => appeal.status === 'rejected').length,
  }
}
