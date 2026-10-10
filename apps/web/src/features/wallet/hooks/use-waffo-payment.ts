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
import { useState, useCallback } from 'react'
import { toast } from 'sonner'

import { requestCreditWaffoPayment, isApiSuccess } from '../api'
import {
  cancelPaymentCheckout,
  redirectToPaymentCheckout,
  reservePaymentCheckout,
} from '../lib'

function getPaymentUrl(data: unknown): string | null {
  if (!data || typeof data !== 'object') {
    return null
  }

  if ('payment_url' in data && typeof data.payment_url === 'string') {
    return data.payment_url
  }

  return null
}

function getErrorMessage(message: string | undefined, data: unknown): string {
  if (typeof data === 'string' && data.trim()) {
    return data
  }

  return message || i18next.t('Payment request failed')
}

/**
 * Hook for handling Waffo payment processing
 */
export function useWaffoPayment() {
  const [processing, setProcessing] = useState(false)

  const processWaffoPayment = useCallback(
    async (topupAmount: number, payMethodIndex?: number, discountCode = '') => {
      let checkout: ReturnType<typeof reservePaymentCheckout> | null = null

      try {
        setProcessing(true)
        checkout = reservePaymentCheckout()
        const response = await requestCreditWaffoPayment({
          amount: topupAmount,
          pay_method_index: payMethodIndex,
          ...(discountCode ? { discount_code: discountCode } : {}),
        })

        if (isApiSuccess(response)) {
          const paymentUrl = getPaymentUrl(response.data)

          if (paymentUrl) {
            if (!redirectToPaymentCheckout(checkout, paymentUrl)) {
              cancelPaymentCheckout(checkout)
              toast.error(i18next.t('Invalid payment redirect URL'))
              return false
            }
            toast.success(i18next.t('Redirecting to payment page...'))
            return true
          }
        }

        cancelPaymentCheckout(checkout)
        toast.error(getErrorMessage(response.message, response.data))
        return false
      } catch {
        if (checkout) cancelPaymentCheckout(checkout)
        toast.error(i18next.t('Payment request failed'))
        return false
      } finally {
        setProcessing(false)
      }
    },
    []
  )

  return { processing, processWaffoPayment }
}
