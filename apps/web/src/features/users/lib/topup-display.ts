/*
Copyright (C) 2026 LIghtJUNction
*/

export type TopupRecord = {
  quota: number
  normalized_quota?: number
  quota_projection_available?: boolean
  money_micros: number
  settled_money_micros?: number
  historical_money_micros?: number
  settled_orders?: number
  historical_orders?: number
  payment_basis?: 'settled' | 'historical' | 'mixed' | 'none'
  orders: number
}

function nonnegativeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
}

/** Only the server's verified projection carries the current credit unit. */
export function currentTopupCredits(record?: TopupRecord): number | null {
  return record?.quota_projection_available === true &&
    nonnegativeInteger(record.normalized_quota)
    ? record.normalized_quota
    : null
}

/** Legacy DTOs expose recorded order money, without evidence of settlement. */
export function topupPaymentAmounts(record?: TopupRecord): {
  settled: number | null
  historical: number | null
} {
  if (!record || record.orders === 0) {
    return { settled: null, historical: null }
  }
  if (record.payment_basis === undefined) {
    return {
      settled: null,
      historical: nonnegativeInteger(record.money_micros)
        ? record.money_micros
        : null,
    }
  }
  const settled =
    (record.payment_basis === 'settled' || record.payment_basis === 'mixed') &&
    nonnegativeInteger(record.settled_orders) &&
    record.settled_orders > 0 &&
    nonnegativeInteger(record.settled_money_micros)
      ? record.settled_money_micros
      : null
  const historical =
    (record.payment_basis === 'historical' ||
      record.payment_basis === 'mixed') &&
    nonnegativeInteger(record.historical_orders) &&
    record.historical_orders > 0 &&
    nonnegativeInteger(record.historical_money_micros)
      ? record.historical_money_micros
      : null
  return { settled, historical }
}
