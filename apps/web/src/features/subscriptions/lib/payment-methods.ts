/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { PlanRecord } from '../types'

export const SUBSCRIPTION_BALANCE_PAYMENT_METHOD = 'balance'

function uniqueMethods(methods: Array<string | null | undefined>): string[] {
  return [...new Set(methods.filter((method): method is string => !!method))]
}

/**
 * Returns the server-authoritative catalog when present. Older servers omitted
 * payment_methods, so only that legacy shape falls back to plan fields.
 */
export function getAdminPlanPaymentMethods(record: PlanRecord): string[] {
  if (Array.isArray(record.payment_methods)) {
    return uniqueMethods(record.payment_methods)
  }

  const { plan } = record
  return uniqueMethods([
    plan.allow_balance_pay !== false
      ? SUBSCRIPTION_BALANCE_PAYMENT_METHOD
      : null,
    plan.stripe_price_id ? 'stripe' : null,
    plan.creem_product_id ? 'creem' : null,
    plan.waffo_pancake_product_id ? 'waffo_pancake' : null,
  ])
}

export function isPlanBalancePaymentAvailable(record: PlanRecord): boolean {
  if (Array.isArray(record.payment_methods)) {
    return record.payment_methods.includes(SUBSCRIPTION_BALANCE_PAYMENT_METHOD)
  }
  return record.plan.allow_balance_pay !== false
}

export function getSubscriptionPaymentMethodLabel(
  method: string,
  translate: (key: string) => string
): string {
  switch (method) {
    case SUBSCRIPTION_BALANCE_PAYMENT_METHOD:
      return translate('Balance')
    case 'stripe':
      return 'Stripe'
    case 'creem':
      return 'Creem'
    case 'waffo_pancake':
      return 'Waffo Pancake'
    default:
      return `ePay · ${method}`
  }
}
