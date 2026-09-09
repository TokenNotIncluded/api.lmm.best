/** Server-owned fiat settlement. Never derive this from platform credit rates. */
export interface SettlementQuote {
  amount: string
  currency: 'CNY' | 'USD'
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
  return { amount, currency }
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
