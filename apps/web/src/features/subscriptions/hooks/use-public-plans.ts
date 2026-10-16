/*
Copyright (C) 2026 LIghtJUNction
*/
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { useCheckoutScope } from '@/features/wallet/hooks/use-checkout-scope'
import { getWaffoPancakeCheckoutLanguage } from '@/lib/waffo-pancake-checkout'

import { getPublicPlans } from '../api'

// @mark payments.settlement-locale role=preview
export function usePublicPlans(enabled = true) {
  const { key } = useCheckoutScope()
  const { i18n } = useTranslation()
  const checkoutLanguage = getWaffoPancakeCheckoutLanguage(
    i18n.resolvedLanguage || i18n.language
  )
  return useQuery({
    queryKey: ['subscription-plans', key],
    queryFn: async ({ signal }) => {
      const response = await getPublicPlans(signal, checkoutLanguage)
      if (!response.success || !Array.isArray(response.data)) {
        throw new Error(
          response.message || 'Failed to fetch subscription plans'
        )
      }
      return response.data
    },
    enabled,
    // Never inherit a global keepPreviousData policy across checkout owners.
    placeholderData: () => undefined,
    staleTime: 0,
    gcTime: 0,
    retry: false,
  })
}
