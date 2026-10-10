/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { api } from '@/lib/api'

import type {
  StoreAnalyticsAuthScope,
  StoreAnalyticsConfig,
  StoreAnalyticsDays,
  StoreAnalyticsPage,
  StoreAnalyticsScope,
} from './analytics-types'

type Envelope<T> = { success: boolean; message?: string; data: T }
const options = {
  skipErrorHandler: true,
  skipBusinessError: true,
  disableDuplicate: true,
}

async function unwrap<T>(request: Promise<{ data: Envelope<T> }>) {
  let response: { data: Envelope<T> }
  try {
    response = await request
  } catch (error) {
    const failure = error as {
      name?: string
      code?: string
      response?: { data?: { message?: unknown } }
    }
    if (
      failure?.name === 'AbortError' ||
      failure?.name === 'CanceledError' ||
      failure?.code === 'ERR_CANCELED'
    ) {
      throw error
    }
    const message = failure?.response?.data?.message
    throw new Error(
      typeof message === 'string' && message ? message : 'Store request failed'
    )
  }
  if (response.data.success !== true) {
    throw new Error(response.data.message || 'Store request failed')
  }
  return response.data.data
}

export const storeAnalyticsApi = {
  read: (
    scope: StoreAnalyticsScope,
    days: StoreAnalyticsDays,
    page: number,
    authScope: StoreAnalyticsAuthScope,
    signal?: AbortSignal
  ) =>
    unwrap<StoreAnalyticsPage>(
      api.get(
        scope === 'all' ? '/api/store/analytics' : '/api/store/my/analytics',
        {
          ...options,
          authScope,
          signal,
          params: { days, offset: (page - 1) * 20, limit: 20 },
        }
      )
    ),
  config: (authScope: StoreAnalyticsAuthScope, signal?: AbortSignal) =>
    unwrap<StoreAnalyticsConfig>(
      api.get('/api/store/analytics/config', { ...options, authScope, signal })
    ),
  saveConfig: (
    body: StoreAnalyticsConfig,
    authScope: StoreAnalyticsAuthScope
  ) =>
    unwrap<StoreAnalyticsConfig>(
      api.put('/api/store/analytics/config', body, { ...options, authScope })
    ),
}
