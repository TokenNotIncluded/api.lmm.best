/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery } from '@tanstack/react-query'

import { storeApi } from './api'

export function useStoreCategories(enabled: boolean) {
  return useQuery({
    queryKey: ['store', 'categories'],
    queryFn: storeApi.categories,
    enabled,
    retry: false,
  })
}
