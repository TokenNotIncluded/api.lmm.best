/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { api, type ApiRequestConfig } from '@/lib/api'

export type StoreTrafficKind = 'impression' | 'click'

export const storeTrafficApi = {
  async record(
    productId: string,
    body: {
      kind: StoreTrafficKind
      page_key: string
      page_started_at: number
    },
    authScope: NonNullable<ApiRequestConfig['authScope']>
  ): Promise<void> {
    const response = await api.post<{ success: boolean }>(
      `/api/store/products/${encodeURIComponent(productId)}/analytics`,
      body,
      {
        skipErrorHandler: true,
        skipBusinessError: true,
        skipAuthRefresh: true,
        disableDuplicate: true,
        authScope,
      }
    )
    if (response.data.success !== true) throw new Error('Store request failed')
  },
}
