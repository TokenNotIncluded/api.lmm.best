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

import { requestCreemPayment, isApiSuccess } from '../api'
import {
  cancelPaymentCheckout,
  redirectToPaymentCheckout,
  reservePaymentCheckout,
} from '../lib'
import { useCheckoutScope } from './use-checkout-scope'

/**
 * Hook for handling Creem payment processing
 */
export function useCreemPayment() {
  const [processing, setProcessing] = useState(false)
  const { isCurrent } = useCheckoutScope()

  const processCreemPayment = useCallback(
    async (productId: string) => {
      if (!isCurrent()) return false
      let checkout: ReturnType<typeof reservePaymentCheckout> | null = null
      try {
        setProcessing(true)
        checkout = reservePaymentCheckout()
        const response = await requestCreemPayment({
          product_id: productId,
          payment_method: 'creem',
        })

        if (!isCurrent()) {
          cancelPaymentCheckout(checkout)
          return false
        }

        if (isApiSuccess(response) && response.data?.checkout_url) {
          if (
            !redirectToPaymentCheckout(checkout, response.data.checkout_url)
          ) {
            cancelPaymentCheckout(checkout)
            toast.error(i18next.t('Invalid payment redirect URL'))
            return false
          }
          toast.success(i18next.t('Redirecting to Creem checkout...'))
          return true
        }

        cancelPaymentCheckout(checkout)
        toast.error(response.message || i18next.t('Payment request failed'))
        return false
      } catch {
        if (checkout) cancelPaymentCheckout(checkout)
        if (!isCurrent()) return false
        toast.error(i18next.t('Payment request failed'))
        return false
      } finally {
        setProcessing(false)
      }
    },
    [isCurrent]
  )

  return { processing, processCreemPayment }
}
