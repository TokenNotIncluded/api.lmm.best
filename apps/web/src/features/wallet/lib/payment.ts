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
import type { WaffoPancakeCheckoutOptions } from '@/lib/waffo-pancake-checkout'

import {
  PAYMENT_TYPES,
  DEFAULT_PRESET_MULTIPLIERS,
  DEFAULT_MIN_TOPUP,
} from '../constants'
import type {
  CreemProduct,
  PaymentMethod,
  PresetAmount,
  TopupInfo,
  WaffoPayMethod,
} from '../types'
import { getPaymentCurrencyLabel } from './format'

// ============================================================================
// Payment Processing Functions
// ============================================================================

/**
 * Check if browser is Safari
 */
function isSafariBrowser(): boolean {
  const userAgent = navigator.userAgent
  return (
    userAgent.includes('Safari') &&
    !/(?:Chrome|CriOS|FxiOS|EdgiOS|OPiOS)/.test(userAgent)
  )
}

/**
 * Submit payment form (for non-Stripe payments)
 */
export function submitPaymentForm(
  url: string,
  params: Record<string, unknown>,
  target?: string | null
): boolean {
  if (!isSafeHttpCheckoutUrl(url)) {
    return false
  }

  const form = document.createElement('form')
  form.action = url
  form.method = 'POST'

  if (target) {
    form.target = target
  } else if (target === undefined && !isSafariBrowser()) {
    // Preserve the legacy behavior for callers that do not reserve a window.
    form.target = '_blank'
  }

  // Add form parameters
  Object.entries(params).forEach(([key, value]) => {
    const input = document.createElement('input')
    input.type = 'hidden'
    input.name = key
    input.value = String(value)
    form.appendChild(input)
  })

  document.body.appendChild(form)
  form.submit()
  document.body.removeChild(form)
  return true
}

/** Reject relative and non-http(s) redirect targets returned by a gateway. */
export function isSafeHttpCheckoutUrl(value: unknown): value is string {
  if (typeof value !== 'string' || !value.trim()) {
    return false
  }

  try {
    const url = new URL(value)
    return url.protocol === 'http:' || url.protocol === 'https:'
  } catch {
    return false
  }
}

export type PaymentCheckout = {
  /** A reserved popup target, or null when the safe fallback is same-tab. */
  target: string | null
  popup: Window | null
}

/**
 * Reserve a popup while the click is still a user gesture. Gateway responses
 * arrive asynchronously, so opening a new window after awaiting them is
 * routinely blocked by browsers. Safari keeps the legacy same-tab flow.
 */
export function reservePaymentCheckout(): PaymentCheckout {
  if (isSafariBrowser()) {
    return { target: null, popup: null }
  }

  const target = `payment_checkout_${Date.now()}_${Math.random()
    .toString(36)
    .slice(2)}`
  const popup = window.open()

  // Defense in depth before the blank window navigates to the checkout.
  // The checkout must never receive a reference to the application window.
  if (popup) {
    try {
      popup.name = target
      popup.opener = null
    } catch {
      // Cross-origin browser implementations may expose a read-only opener.
    }
  }

  return popup ? { target, popup } : { target: null, popup: null }
}

/** Close an unused reserved popup after a failed payment request. */
export function cancelPaymentCheckout(checkout: PaymentCheckout): void {
  checkout.popup?.close()
}

/**
 * Navigate a reserved checkout window. If the browser declined the popup,
 * fall back to a safe same-tab navigation rather than trying another blocked
 * popup after the async request completes.
 */
function navigateCurrentWindow(url: string) {
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.target = '_self'
  anchor.rel = 'noopener noreferrer'
  document.body.appendChild(anchor)
  anchor.click()
  document.body.removeChild(anchor)
}

export function redirectToPaymentCheckout(
  checkout: PaymentCheckout,
  url: unknown
): boolean {
  if (!isSafeHttpCheckoutUrl(url)) {
    return false
  }

  if (checkout.popup && !checkout.popup.closed) {
    checkout.popup.location.href = url
    checkout.popup.focus()
  } else {
    navigateCurrentWindow(url)
  }

  return true
}

/**
 * Check if payment method is Stripe
 */
export function isStripePayment(paymentType: string): boolean {
  return paymentType === PAYMENT_TYPES.STRIPE
}

/**
 * Check if payment method is Creem
 */
function isCreemPayment(paymentType: string): boolean {
  return paymentType === PAYMENT_TYPES.CREEM
}

/**
 * Generic ePay-family methods for the shared form submission flow.
 *
 * Dedicated gateways (Stripe, Creem, Waffo, Waffo Pancake) render their own
 * buttons and checkouts and must never be offered through the generic ePay
 * form, otherwise the client sends `payment_method: "waffo_pancake"` to the
 * plain ePay endpoint, which has no such method configured.
 */
export function getEpayMethods(
  payMethods: PaymentMethod[] = []
): PaymentMethod[] {
  return payMethods.filter(
    (m) =>
      m?.type &&
      !isStripePayment(m.type) &&
      !isCreemPayment(m.type) &&
      !isWaffoPayment(m.type) &&
      !isWaffoPancakePayment(m.type)
  )
}

/**
 * Check if payment method is Waffo
 */
export function isWaffoPayment(paymentType: string): boolean {
  return paymentType === PAYMENT_TYPES.WAFFO
}

/**
 * Check if payment method is Waffo Pancake
 *
 * Pancake is a metered-style payment that goes through a dedicated checkout
 * URL flow rather than the generic epay form submission, so it must be
 * special-cased in payment dispatch logic.
 */
export function isWaffoPancakePayment(paymentType: string): boolean {
  return paymentType === PAYMENT_TYPES.WAFFO_PANCAKE
}

/**
 * The frozen Go Waffo Pancake checkout always creates USD orders. Do not let
 * a CNY-configured page label that USD order as CNY or send it to checkout.
 */
export function isWaffoPancakeCurrencySupported(): boolean {
  return getPaymentCurrencyLabel() === 'USD'
}

/** Provider-specific currency policy; other gateway providers retain CNY. */
export function isPaymentMethodCurrencySupported(paymentType: string): boolean {
  return (
    !isWaffoPancakePayment(paymentType) || isWaffoPancakeCurrencySupported()
  )
}

export interface PaymentProcessors {
  regular: (
    topupAmount: number,
    paymentType: string,
    discountCode?: string
  ) => Promise<boolean>
  waffo: (
    topupAmount: number,
    payMethodIndex: number,
    discountCode?: string
  ) => Promise<boolean>
  waffoPancake: (
    topupAmount: number,
    checkoutOptions?: WaffoPancakeCheckoutOptions & { discount_code?: string }
  ) => Promise<boolean>
}

export async function dispatchSelectedPayment(
  paymentMethod: PaymentMethod,
  topupAmount: number,
  waffoMethodIndex: number | null,
  processors: PaymentProcessors,
  waffoPancakeCheckoutOptions?: WaffoPancakeCheckoutOptions & {
    discount_code?: string
  },
  discountCode = ''
): Promise<boolean> {
  if (isWaffoPayment(paymentMethod.type)) {
    if (waffoMethodIndex === null) {
      return false
    }
    return processors.waffo(topupAmount, waffoMethodIndex, discountCode)
  }

  if (isWaffoPancakePayment(paymentMethod.type)) {
    if (!waffoPancakeCheckoutOptions) {
      return processors.waffoPancake(topupAmount)
    }
    return processors.waffoPancake(topupAmount, {
      ...waffoPancakeCheckoutOptions,
      ...(discountCode ? { discount_code: discountCode } : {}),
    })
  }

  return processors.regular(topupAmount, paymentMethod.type, discountCode)
}

export interface TopupAvailability {
  standardMethods: PaymentMethod[]
  waffoMethods: WaffoPayMethod[]
  creemProducts: CreemProduct[]
  defaultQuotedType: string | null
  hasPaymentMethod: boolean
}

/**
 * Normalize the advertised top-up configuration into payment methods that can
 * actually be used. Provider flags alone are not sufficient: every flow also
 * needs its corresponding method or product configuration.
 */
export function getTopupAvailability(
  topupInfo: TopupInfo | null
): TopupAvailability {
  if (!topupInfo) {
    return {
      standardMethods: [],
      waffoMethods: [],
      creemProducts: [],
      defaultQuotedType: null,
      hasPaymentMethod: false,
    }
  }

  const standardMethods = (topupInfo.pay_methods ?? []).filter((method) => {
    if (!method?.name || !method.type) return false
    if (!isPaymentMethodCurrencySupported(method.type)) return false

    if (isStripePayment(method.type)) {
      return topupInfo.enable_stripe_topup === true
    }
    if (isWaffoPancakePayment(method.type)) {
      return topupInfo.enable_waffo_pancake_topup === true
    }
    if (isCreemPayment(method.type) || isWaffoPayment(method.type)) {
      return false
    }

    return topupInfo.enable_online_topup === true
  })
  const waffoMethods = topupInfo.enable_waffo_topup
    ? (topupInfo.waffo_pay_methods ?? []).filter((method) => method?.name)
    : []
  const creemProducts = topupInfo.enable_creem_topup
    ? (topupInfo.creem_products ?? []).filter(
        (product) => product?.name && product.productId
      )
    : []
  const defaultQuotedType =
    standardMethods[0]?.type ??
    (waffoMethods.length > 0 ? PAYMENT_TYPES.WAFFO : null)

  return {
    standardMethods,
    waffoMethods,
    creemProducts,
    defaultQuotedType,
    hasPaymentMethod:
      standardMethods.length > 0 ||
      waffoMethods.length > 0 ||
      creemProducts.length > 0,
  }
}

/**
 * Get default payment type from topup info
 */
export function getDefaultPaymentType(
  topupInfo: TopupInfo | null
): string | null {
  return getTopupAvailability(topupInfo).defaultQuotedType
}

/**
 * Get minimum topup amount from topup info
 */
export function getMinTopupAmount(topupInfo: TopupInfo | null): number {
  if (!topupInfo) {
    return DEFAULT_MIN_TOPUP
  }

  const paymentType = getTopupAvailability(topupInfo).defaultQuotedType

  if (paymentType === PAYMENT_TYPES.STRIPE) {
    return topupInfo.stripe_min_topup
  }

  if (paymentType === PAYMENT_TYPES.WAFFO) {
    return topupInfo.waffo_min_topup || DEFAULT_MIN_TOPUP
  }

  if (paymentType === PAYMENT_TYPES.WAFFO_PANCAKE) {
    return topupInfo.waffo_pancake_min_topup || DEFAULT_MIN_TOPUP
  }

  if (paymentType) return topupInfo.min_topup

  return DEFAULT_MIN_TOPUP
}

/**
 * Generate preset amounts based on minimum topup
 */
export function generatePresetAmounts(minAmount: number): PresetAmount[] {
  return DEFAULT_PRESET_MULTIPLIERS.map((multiplier) => ({
    value: minAmount * multiplier,
  }))
}

/**
 * Merge custom preset amounts with discounts
 */
export function mergePresetAmounts(
  amountOptions: number[],
  discounts: Record<number, number>
): PresetAmount[] {
  if (!amountOptions || amountOptions.length === 0) {
    return []
  }

  return amountOptions.map((amount) => ({
    value: amount,
    discount: discounts[amount] || 1.0,
  }))
}
