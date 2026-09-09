import { useQuery, useQueryClient } from '@tanstack/react-query'
/*
Copyright (C) 2026 LIghtJUNction
*/
import { useState } from 'react'

import { getSelf } from '@/lib/api'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { getSmsPurchaseBalance } from './sms-balance'

export function useSmsPurchaseBalance() {
  const userId = useAuthStore((state) => state.auth.user?.id)
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  const quotaPerUnit = useSystemConfigStore(
    (state) => state.config.currency.quotaPerUnit
  )
  const queryClient = useQueryClient()
  const queryKey = ['user', 'sms-purchase-balance', userId, sessionId] as const
  const [denial, setDenial] = useState<{
    userId: number | undefined
    sessionId: string | undefined
    version: number
  } | null>(null)
  const isCurrentSession = () => {
    const current = useAuthStore.getState().auth
    return (
      userId !== undefined &&
      current.user?.id === userId &&
      current.session?.sid === sessionId
    )
  }
  const balance = useQuery({
    queryKey,
    enabled: userId !== undefined,
    staleTime: 0,
    retry: false,
    refetchOnMount: 'always',
    refetchOnWindowFocus: 'always',
    queryFn: async ({ signal }) => {
      const response = await getSelf()
      const user = response?.data as AuthUser | undefined
      if (
        signal.aborted ||
        !isCurrentSession() ||
        !response?.success ||
        !user ||
        user.id !== userId ||
        typeof user.quota !== 'number' ||
        !Number.isFinite(user.quota)
      ) {
        throw new Error('Wallet balance unavailable')
      }
      const current = useAuthStore.getState().auth
      if (current.user) current.setUser({ ...current.user, quota: user.quota })
      return user.quota
    },
  })
  const serverDenied =
    denial?.userId === userId &&
    denial?.sessionId === sessionId &&
    denial?.version === balance.dataUpdatedAt
  const eligibility = getSmsPurchaseBalance(
    balance.isError || serverDenied ? undefined : balance.data,
    quotaPerUnit
  )

  return {
    ...eligibility,
    serverDenied,
    canPurchase: eligibility.status === 'allowed',
    isLoading: balance.isPending && balance.isFetching,
    isRefreshing: balance.isFetching,
    isCurrentSession,
    refresh: async () => {
      if (!isCurrentSession()) return false
      const result = await balance.refetch()
      if (!isCurrentSession() || result.isError) return false
      setDenial(null)
      return (
        getSmsPurchaseBalance(queryClient.getQueryData(queryKey), quotaPerUnit)
          .status === 'allowed'
      )
    },
    markDenied: () => {
      if (!isCurrentSession()) return
      setDenial({ userId, sessionId, version: balance.dataUpdatedAt })
      void balance.refetch()
    },
    recordQuota: (quota: number) => {
      if (!Number.isFinite(quota) || !isCurrentSession()) return
      // A balance read started before this purchase/refund must not overwrite
      // the newer settlement when its response eventually arrives.
      void queryClient.cancelQueries({ queryKey, exact: true })
      queryClient.setQueryData(queryKey, quota)
      const current = useAuthStore.getState().auth
      if (current.user) current.setUser({ ...current.user, quota })
    },
  }
}
