/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
import { useQueries } from '@tanstack/react-query'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import type { ModelRuntimeState } from '../types'

export function useModelRuntime(names: string[], enabled = true) {
  const user = useAuthStore((state) => state.auth.user)
  const sorted = [...new Set(names)].sort()
  const batches: string[][] = []
  for (let i = 0; i < sorted.length; i += 200) {
    batches.push(sorted.slice(i, i + 200))
  }
  return useQueries({
    queries: batches.map((models) => ({
      queryKey: [
        'model-runtime',
        user?.id ?? 0,
        user?.group ?? '',
        user?.developer_access_granted,
        models,
      ],
      queryFn: async ({ signal }) => {
        const response = await api.post<{
          success: boolean
          data: Record<string, ModelRuntimeState>
        }>(
          '/api/pricing/runtime',
          { models },
          {
            signal,
            timeout: 10_000,
            skipErrorHandler: true,
            skipBusinessError: true,
          }
        )
        if (!response.data.success) {
          throw new Error('Model runtime state is unavailable')
        }
        return response.data.data
      },
      enabled,
      staleTime: 15_000,
      refetchInterval: 30_000,
      retry: false,
    })),
    combine: (queries) =>
      Object.assign(
        {},
        ...queries.map((query) => (query.isError ? {} : (query.data ?? {})))
      ) as Record<string, ModelRuntimeState>,
  })
}
