/*
Copyright (C) 2026 LIghtJUNction

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { registrationState } from './assistant-registration-state'

export function useAssistantRegistrationState() {
  const user = useAuthStore((state) => state.auth.user)
  const sid = useAuthStore((state) => state.auth.session?.sid)
  return useQuery({
    queryKey: ['assistant-registration-state', user?.id, sid],
    queryFn: async ({ signal }) => {
      const { data } = await api.get<{
        success: boolean
        data?: { state?: unknown }
      }>('/api/assistant/registration-check', {
        signal,
        disableDuplicate: true,
        skipBusinessError: true,
        skipErrorHandler: true,
      })
      if (!data.success || !data.data) {
        throw new Error('Registration status is unavailable')
      }
      return registrationState(data.data.state)
    },
    enabled: Boolean(user) && user?.developer_access_granted !== true,
    retry: false,
    staleTime: 5_000,
    refetchInterval: (query) =>
      query.state.data === 'active' || query.state.error ? false : 15_000,
    refetchIntervalInBackground: false,
  })
}
