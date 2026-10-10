/* Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later */
import { useQuery } from '@tanstack/react-query'

import { catalogueApi } from './catalogue-api'
import { useStoreViewer } from './store-viewer'

export function useStoreCatalogueSupport() {
  const viewer = useStoreViewer()
  const query = useQuery({
    queryKey: ['store', 'catalogue-support', viewer],
    queryFn: ({ signal }) => catalogueApi.config(signal),
    staleTime: 60_000,
    retry: false,
  })
  return {
    ...query,
    catalogueSupported: query.data?.store_catalogue_supported === true,
    collectionsSupported: query.data?.store_collections_supported === true,
  }
}
