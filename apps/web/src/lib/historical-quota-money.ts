/*
Copyright (C) 2026 LIghtJUNction
*/

export interface HistoricalQuotaProjection {
  quota_usd?: string
  quota_money_available?: boolean
  quota_money_basis?: string
  unpriced_quota_records?: number
  quota_projection_truncated?: boolean
}

/** Missing historical units never acquire today's wallet or FX denomination. */
export function formatHistoricalRawQuota(
  quota: number | string | bigint | null | undefined,
  unit: string,
  locale?: ConstructorParameters<typeof Intl.NumberFormat>[0]
): string {
  let exact: number | bigint
  if (typeof quota === 'number' && Number.isSafeInteger(quota)) {
    exact = quota
  } else if (typeof quota === 'bigint') {
    exact = quota
  } else if (
    typeof quota === 'string' &&
    quota.length <= 128 &&
    /^-?(?:0|[1-9]\d*)$/.test(quota)
  ) {
    exact = BigInt(quota)
  } else {
    return '—'
  }
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(exact)} ${unit}`
}

/** An absent stored amount is not evidence of a real zero-credit entry. */
export function formatHistoricalQuotaFallback(
  quota: number,
  projection: HistoricalQuotaProjection | null | undefined,
  unit: string,
  locale?: ConstructorParameters<typeof Intl.NumberFormat>[0]
): string {
  return projection?.quota_money_basis === 'invalid_stored_quota'
    ? '—'
    : formatHistoricalRawQuota(quota, unit, locale)
}

/** The API supplies a complete projection, never an incomplete priced subtotal. */
export function historicalQuotaUSD(
  projection: HistoricalQuotaProjection | null | undefined
): number | null {
  const usd = projection?.quota_usd
  if (
    projection?.quota_money_available !== true ||
    projection.quota_projection_truncated === true ||
    (projection.unpriced_quota_records !== undefined &&
      projection.unpriced_quota_records !== 0) ||
    !['captured_credits_per_usd', 'empty_result'].includes(
      projection.quota_money_basis ?? ''
    ) ||
    typeof usd !== 'string' ||
    usd.length > 128 ||
    !/^(?:0|[1-9]\d*)(?:\.\d+)?$/.test(usd)
  ) {
    return null
  }
  const value = Number(usd)
  if (!Number.isFinite(value) || value < 0 || value > Number.MAX_SAFE_INTEGER) {
    return null
  }
  if (projection.quota_money_basis === 'empty_result' && value !== 0) {
    return null
  }
  return value
}

function capturedQuotaUSD(quota: number, other: unknown): number | null {
  if (!Number.isSafeInteger(quota) || quota < 0) return null
  if (typeof other !== 'object' || other === null || Array.isArray(other)) {
    return null
  }
  const anchor = (other as { pricing_credits_per_usd?: unknown })
    .pricing_credits_per_usd
  return typeof anchor === 'number' &&
    Number.isSafeInteger(anchor) &&
    anchor > 0
    ? quota / anchor
    : null
}

/** Legacy responses can use this entry's numeric captured K, never global K. */
export function historicalLogQuotaUSD(input: {
  quota: number
  other: unknown
  type?: number
  projection?: HistoricalQuotaProjection
  consumedQuota?: number
}): number | null {
  if (input.type !== undefined && input.type !== 2) return null
  const projection = input.projection
  const explicitProjection =
    projection?.quota_money_available !== undefined ||
    projection?.quota_money_basis !== undefined ||
    projection?.quota_usd !== undefined ||
    projection?.quota_projection_truncated !== undefined ||
    projection?.unpriced_quota_records !== undefined
  const consumed = input.consumedQuota ?? input.quota
  if (explicitProjection) {
    if (projection?.quota_money_basis !== 'captured_credits_per_usd') {
      return null
    }
    const projected = historicalQuotaUSD(projection)
    if (projected === null) return null
    return consumed === input.quota
      ? projected
      : capturedQuotaUSD(consumed, input.other)
  }
  return capturedQuotaUSD(consumed, input.other)
}
