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
import i18next from 'i18next'
import { useCallback, useRef, useState } from 'react'
import { toast } from 'sonner'

import { isLocalPreview } from '@/lib/local-preview'

import {
  calculateAmount,
  calculateStripeAmount,
  calculateWaffoAmount,
  calculateWaffoPancakeAmount,
  requestPayment,
  requestStripePayment,
  isApiSuccess,
} from '../api'
import {
  isStripePayment,
  isWaffoPayment,
  isWaffoPancakePayment,
  cancelPaymentCheckout,
  isSafeHttpCheckoutUrl,
  redirectToPaymentCheckout,
  reservePaymentCheckout,
  submitPaymentForm,
} from '../lib'
import {
  parseSettlementQuote,
  type SettlementQuote,
} from '../lib/settlement-quote'
import type { AmountRequest, AmountResponse, PaymentResponse } from '../types'
import { useCheckoutScope } from './use-checkout-scope'

// ============================================================================
// Payment Hook
// ============================================================================

type AmountCalculator = (request: AmountRequest) => Promise<AmountResponse>

export interface PaymentAmountCalculators {
  regular: AmountCalculator
  stripe: AmountCalculator
  waffo: AmountCalculator
  waffoPancake: AmountCalculator
}

const defaultPaymentAmountCalculators: PaymentAmountCalculators = {
  regular: calculateAmount,
  stripe: calculateStripeAmount,
  waffo: calculateWaffoAmount,
  waffoPancake: calculateWaffoPancakeAmount,
}

export async function requestPaymentQuote(
  topupAmount: number,
  paymentType: string,
  discountCodeOrCalculators: string | PaymentAmountCalculators = '',
  providedCalculators?: PaymentAmountCalculators
): Promise<{ amount: number; settlementQuote: SettlementQuote | null }> {
  // Keep the old third-argument calculators form working for callers outside
  // the wallet while allowing the wallet to pass a discount code.
  const discountCode =
    typeof discountCodeOrCalculators === 'string'
      ? discountCodeOrCalculators
      : ''
  const calculators =
    typeof discountCodeOrCalculators === 'string'
      ? (providedCalculators ?? defaultPaymentAmountCalculators)
      : discountCodeOrCalculators
  const usesRegularCalculator =
    !isStripePayment(paymentType) &&
    !isWaffoPayment(paymentType) &&
    !isWaffoPancakePayment(paymentType)
  let calculator = calculators.regular
  if (isStripePayment(paymentType)) {
    calculator = calculators.stripe
  } else if (isWaffoPayment(paymentType)) {
    calculator = calculators.waffo
  } else if (isWaffoPancakePayment(paymentType)) {
    calculator = calculators.waffoPancake
  }

  const request = usesRegularCalculator
    ? {
        amount: topupAmount,
        payment_method: paymentType,
        ...(discountCode ? { discount_code: discountCode } : {}),
      }
    : {
        amount: topupAmount,
        ...(discountCode ? { discount_code: discountCode } : {}),
      }
  const response = await calculator(request)
  const unavailable = { amount: 0, settlementQuote: null }
  if (!isApiSuccess(response) || !response.data) return unavailable
  if (isWaffoPancakePayment(paymentType)) {
    const settlementQuote = parseSettlementQuote({
      amount: response.data,
      currency: response.settlement_currency,
    })
    return settlementQuote
      ? { amount: Number(settlementQuote.amount), settlementQuote }
      : unavailable
  }
  return { amount: Number.parseFloat(response.data), settlementQuote: null }
}

export async function requestPaymentAmount(
  ...args: Parameters<typeof requestPaymentQuote>
): Promise<number> {
  return (await requestPaymentQuote(...args)).amount
}

export { isPositivePaymentAmount } from '../lib/payment'

export function usePayment() {
  const { key: scope, isCurrent } = useCheckoutScope()
  const [quote, setQuote] = useState<{
    scope: string
    amount: number
    settlementQuote: SettlementQuote | null
  } | null>(null)
  const amount = quote?.scope === scope ? quote.amount : 0
  const settlementQuote = quote?.scope === scope ? quote.settlementQuote : null
  const [calculatingScope, setCalculatingScope] = useState<string | null>(null)
  const calculating = calculatingScope === scope
  const [processing, setProcessing] = useState(false)
  const amountRequestIdRef = useRef(0)
  const localPreview = isLocalPreview()
  const invalidateQuote = useCallback(() => {
    ++amountRequestIdRef.current
    setQuote(null)
    setCalculatingScope(null)
  }, [])
  // Scope-derived state hides old quotes immediately. useCheckoutScope also
  // rejects completions after unmount, so no synchronous effect reset is needed.

  // Calculate payment amount. Only the newest request may update the quote;
  // slower responses for an earlier amount must not overwrite current state.
  const calculatePaymentAmount = useCallback(
    async (topupAmount: number, paymentType: string, discountCode = '') => {
      const requestId = ++amountRequestIdRef.current
      if (localPreview && !isWaffoPancakePayment(paymentType)) {
        setQuote({ scope, amount: topupAmount, settlementQuote: null })
        return topupAmount
      }

      setQuote(null)
      setCalculatingScope(scope)

      try {
        const calculated = await requestPaymentQuote(
          topupAmount,
          paymentType,
          discountCode
        )
        // Callers also use this result to open checkout confirmation. A stale
        // success must not approve the currently selected amount or method.
        if (requestId !== amountRequestIdRef.current || !isCurrent()) return 0
        setQuote({ scope, ...calculated })
        return calculated.amount
      } catch {
        if (requestId === amountRequestIdRef.current && isCurrent()) {
          setQuote(null)
        }
        return 0
      } finally {
        if (requestId === amountRequestIdRef.current && isCurrent()) {
          setCalculatingScope(null)
        }
      }
    },
    [localPreview, scope, isCurrent]
  )

  // Process payment
  const processPayment = useCallback(
    async (topupAmount: number, paymentType: string, discountCode = '') => {
      if (localPreview) {
        toast.info(
          i18next.t(
            'Local preview only: no payment is started and no balance is changed.'
          )
        )
        return false
      }

      let checkout: ReturnType<typeof reservePaymentCheckout> | null = null
      try {
        setProcessing(true)

        const isStripe = isStripePayment(paymentType)
        const amount = Math.floor(topupAmount)
        checkout = reservePaymentCheckout()

        const response = isStripe
          ? await requestStripePayment({
              amount,
              payment_method: 'stripe',
              ...(discountCode ? { discount_code: discountCode } : {}),
            })
          : await requestPayment({
              amount,
              payment_method: paymentType,
              ...(discountCode ? { discount_code: discountCode } : {}),
            })

        if (!isApiSuccess(response)) {
          cancelPaymentCheckout(checkout)
          toast.error(response.message || i18next.t('Payment request failed'))
          return false
        }

        // Handle Stripe payment
        if (isStripe && response.data?.pay_link) {
          if (!redirectToPaymentCheckout(checkout, response.data.pay_link)) {
            cancelPaymentCheckout(checkout)
            toast.error(i18next.t('Invalid payment redirect URL'))
            return false
          }
          toast.success(i18next.t('Redirecting to payment page...'))
          return true
        }

        // Handle non-Stripe payment
        if (!isStripe && response.data) {
          const url = (response as PaymentResponse).url
          if (isSafeHttpCheckoutUrl(url)) {
            if (!submitPaymentForm(url, response.data, checkout.target)) {
              cancelPaymentCheckout(checkout)
              toast.error(i18next.t('Invalid payment redirect URL'))
              return false
            }
            toast.success(i18next.t('Redirecting to payment page...'))
            return true
          }
        }

        cancelPaymentCheckout(checkout)
        toast.error(i18next.t('Invalid payment redirect URL'))
        return false
      } catch {
        if (checkout) {
          cancelPaymentCheckout(checkout)
        }
        toast.error(i18next.t('Payment request failed'))
        return false
      } finally {
        setProcessing(false)
      }
    },
    [localPreview]
  )

  return {
    amount,
    calculating,
    processing,
    calculatePaymentAmount,
    processPayment,
    settlementQuote,
    invalidateQuote,
  }
}
