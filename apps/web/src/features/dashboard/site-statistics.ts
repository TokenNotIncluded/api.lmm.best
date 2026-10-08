/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { z } from 'zod'

import { api } from '@/lib/api'

const positiveInteger = z.string().regex(/^\d+$/)
const count = z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER)
const paymentRow = z.object({
  currency: z.string().regex(/^[A-Z]{3}$/),
  gross_amount_micros: positiveInteger,
  refunded_amount_micros: positiveInteger,
  net_amount_micros: positiveInteger,
  orders: count,
})
const statisticsSchema = z.object({
  as_of: count,
  credits_per_usd: z.literal(500000),
  total_used_credits: positiveInteger,
  total_balance_credits: z.string().regex(/^-?\d+$/),
  recharge: z.object({
    currencies: z.array(
      paymentRow.extend({
        currency: z
          .string()
          .regex(/^[A-Z]{3}$/)
          .refine((currency) => currency !== 'LDC'),
      })
    ),
    virtual_units: z.array(paymentRow.extend({ currency: z.literal('LDC') })),
    confirmed_orders: count,
    unconfirmed_orders: count,
    invalid_orders: count,
  }),
})

export type AdminSiteStatistics = z.infer<typeof statisticsSchema>

export async function getAdminSiteStatistics(): Promise<AdminSiteStatistics> {
  const response = await api.get<{
    success: boolean
    data?: unknown
  }>('/api/finance/site-statistics')
  if (!response.data.success) throw new Error('Site statistics unavailable')
  return statisticsSchema.parse(response.data.data)
}

export function formatSiteCredits(value: string, locale: string): string {
  return new Intl.NumberFormat(locale).format(BigInt(value))
}

export function formatSitePaymentMicros(value: string, locale: string): string {
  const micros = BigInt(value)
  const units = micros / 1_000_000n
  const fraction = (micros % 1_000_000n)
    .toString()
    .padStart(6, '0')
    .replace(/0+$/, '')
    .padEnd(2, '0')
  const decimalSeparator =
    new Intl.NumberFormat(locale)
      .formatToParts(1.1)
      .find((part) => part.type === 'decimal')?.value ?? '.'
  return `${formatSiteCredits(units.toString(), locale)}${decimalSeparator}${fraction}`
}
