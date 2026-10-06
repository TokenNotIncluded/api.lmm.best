/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
export type CumulativeUserUsage = {
  used_quota?: number
  normalized_used_quota?: number | null
  usage_projection_available?: boolean
}

/** Historical raw counters remain facts, but cannot be valued with today's FX. */
export function normalizedUserUsage(
  user: CumulativeUserUsage | null | undefined
): number | null {
  const quota = user?.normalized_used_quota
  return user?.usage_projection_available === true &&
    typeof quota === 'number' &&
    Number.isSafeInteger(quota) &&
    quota >= 0
    ? quota
    : null
}

export function formatCumulativeUserUsage(
  user: CumulativeUserUsage | null | undefined,
  formatNormalized: (quota: number) => string,
  creditLabel: string,
  locale?: ConstructorParameters<typeof Intl.NumberFormat>[0]
): string {
  const normalized = normalizedUserUsage(user)
  if (normalized !== null) return formatNormalized(normalized)
  const raw = user?.used_quota
  if (typeof raw !== 'number' || !Number.isSafeInteger(raw) || raw < 0) {
    return '-'
  }
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(raw)} ${creditLabel}`
}
