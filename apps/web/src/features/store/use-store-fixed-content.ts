/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'

import { useAuthStore } from '@/stores/auth-store'

import { storeApi } from './api'
import { STORE_FIXED_CONTENT_COPY as copy } from './fixed-content-copy'

export function useStoreFixedContentDraft(
  productId?: string,
  variantId?: string,
  existingFixed = false
) {
  const userId = useAuthStore((state) => state.auth.user?.id)
  const [edited, setEdited] = useState<string>()
  const query = useQuery({
    queryKey: ['store', 'fixed-content', userId, productId, variantId],
    queryFn: () => {
      if (!productId || !variantId) throw new Error(copy.invalid)
      return storeApi.variantFixedContent(productId, variantId)
    },
    enabled: existingFixed && !!userId && !!productId && !!variantId,
    retry: false,
    gcTime: 0,
  })
  const content = edited ?? query.data?.content ?? ''
  const loading = existingFixed && query.isPending
  const valid =
    !loading &&
    !query.error &&
    content.trim().length > 0 &&
    new TextEncoder().encode(content).length <= 128 * 1024
  return {
    content,
    setContent: setEdited,
    loading,
    valid,
    error: query.error,
    retry: () => void query.refetch(),
    write: existingFixed ? edited : content,
  }
}
