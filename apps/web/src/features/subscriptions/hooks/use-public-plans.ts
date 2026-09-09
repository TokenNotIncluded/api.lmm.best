/*
Copyright (C) 2026 LIghtJUNction
*/
import { useQuery } from '@tanstack/react-query'

import { useCheckoutScope } from '@/features/wallet/hooks/use-checkout-scope'

import { getPublicPlans } from '../api'

export function usePublicPlans(enabled = true) {
  const { key } = useCheckoutScope()
  return useQuery({
    queryKey: ['subscription-plans', key],
    queryFn: async ({ signal }) => {
      const response = await getPublicPlans(signal)
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
