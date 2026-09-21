/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { useAuthStore } from '@/stores/auth-store'

import { getCompanyBillingProfile, updateCompanyBillingProfile } from './api'

export function companyBillingProfileQueryKey(userId: number) {
  return ['user', userId, 'company-billing-profile'] as const
}

const SIGNED_OUT_COMPANY_BILLING_PROFILE_QUERY_KEY = [
  'user',
  'signed-out',
  'company-billing-profile',
] as const

export function useCompanyBillingProfile() {
  const queryClient = useQueryClient()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const ownerUserId =
    typeof userId === 'number' && Number.isSafeInteger(userId) && userId > 0
      ? userId
      : null
  const hasAuthenticatedOwner = ownerUserId !== null
  const queryKey = hasAuthenticatedOwner
    ? companyBillingProfileQueryKey(ownerUserId)
    : SIGNED_OUT_COMPANY_BILLING_PROFILE_QUERY_KEY
  const query = useQuery({
    queryKey,
    queryFn: ({ signal }) => getCompanyBillingProfile(signal),
    enabled: hasAuthenticatedOwner,
    retry: false,
  })
  const mutation = useMutation({
    mutationFn: updateCompanyBillingProfile,
    onSuccess: (profile) => {
      if (ownerUserId !== null) {
        queryClient.setQueryData(
          companyBillingProfileQueryKey(ownerUserId),
          profile
        )
      }
    },
  })

  return {
    ownerUserId,
    profile: query.data ?? null,
    loading: !hasAuthenticatedOwner || query.isPending,
    loadError: query.isError,
    retrying: query.isFetching,
    retry: query.refetch,
    save: mutation.mutate,
    saving: mutation.isPending,
    saved: mutation.isSuccess,
    saveError: mutation.error,
    resetSave: mutation.reset,
  }
}
