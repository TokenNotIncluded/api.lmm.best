import { formatFiatCurrencyAmount } from '@/lib/currency'

/** The plan's original fiat list price, never a platform-credit or FX conversion. */
export function formatPlanSourcePrice(plan: {
  price_amount: number
  currency?: string
}): string | null {
  if (
    (plan.currency !== 'CNY' && plan.currency !== 'USD') ||
    !Number.isFinite(plan.price_amount) ||
    plan.price_amount < 0
  ) {
    return null
  }
  return formatFiatCurrencyAmount(plan.price_amount, plan.currency, {
    abbreviate: false,
    digitsLarge: 2,
    digitsSmall: 2,
  })
}
