/*
Copyright (C) 2026 LIghtJUNction
*/
/** Server-owned fiat settlement. Never derive this from platform credit rates. */
export interface SettlementQuote {
  amount: string
  currency: 'CNY' | 'USD'
  originalAmount?: string
  savingsAmount?: string
}

export interface ExpectedSettlement {
  settlement_amount?: string
  settlement_currency?: 'CNY' | 'USD'
}

export interface AvailableSettlementQuote extends SettlementQuote {
  available: boolean
  reason?: string
}

export function parseSettlementQuote(value: unknown): SettlementQuote | null {
  if (!value || typeof value !== 'object') return null
  const { amount, currency } = value as Partial<SettlementQuote>
  if (
    typeof amount !== 'string' ||
    !/^[0-9]+(?:\.[0-9]+)?$/.test(amount) ||
    !Number.isFinite(Number(amount)) ||
    Number(amount) <= 0 ||
    (currency !== 'CNY' && currency !== 'USD')
  ) {
    return null
  }
  const originalAmount = readOptionalSettlementAmount(
    (value as { originalAmount?: unknown }).originalAmount
  )
  const savingsAmount = readOptionalSettlementAmount(
    (value as { savingsAmount?: unknown }).savingsAmount
  )
  if (
    (originalAmount !== undefined && Number(originalAmount) < Number(amount)) ||
    (savingsAmount !== undefined && Number(savingsAmount) <= 0)
  ) {
    return null
  }
  return {
    amount,
    currency,
    ...(originalAmount ? { originalAmount } : {}),
    ...(savingsAmount ? { savingsAmount } : {}),
  }
}

function readOptionalSettlementAmount(value: unknown): string | undefined {
  if (value === undefined) return undefined
  if (
    typeof value !== 'string' ||
    !/^[0-9]+(?:\.[0-9]+)?$/.test(value) ||
    !Number.isFinite(Number(value)) ||
    Number(value) <= 0
  ) {
    return undefined
  }
  return value
}

export function getAvailableSettlementQuote(
  value: unknown
): SettlementQuote | null {
  if (
    !value ||
    typeof value !== 'object' ||
    !('available' in value) ||
    value.available !== true
  ) {
    return null
  }
  return parseSettlementQuote(value)
}

/** Preserve the exact decimal string that will be echoed at checkout. */
export function formatSettlementQuote(quote: SettlementQuote): string {
  return `${quote.amount} ${quote.currency}`
}

export function expectedSettlement(
  quote: SettlementQuote
): Required<ExpectedSettlement> {
  return {
    settlement_amount: quote.amount,
    settlement_currency: quote.currency,
  }
}

export class SettlementQuoteChangedError extends Error {
  constructor() {
    super('SETTLEMENT_QUOTE_CHANGED')
    this.name = 'SettlementQuoteChangedError'
  }
}

export function isSettlementQuoteChanged(value: unknown): boolean {
  if (value instanceof SettlementQuoteChangedError) return true
  if (!value || typeof value !== 'object') return false
  const data = value as {
    code?: unknown
    message?: unknown
    error?: { code?: unknown }
    response?: { data?: unknown }
  }
  return (
    data.code === 'SETTLEMENT_QUOTE_CHANGED' ||
    data.message === 'SETTLEMENT_QUOTE_CHANGED' ||
    data.error?.code === 'SETTLEMENT_QUOTE_CHANGED' ||
    (data.response?.data !== undefined &&
      isSettlementQuoteChanged(data.response.data))
  )
}
