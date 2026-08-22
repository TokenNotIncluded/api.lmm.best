/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useMutation, useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'

import {
  cancelHeroSmsActivation,
  createHeroSmsActivations,
  getHeroSmsActivationDetail,
  listHeroSmsActivations,
  listHeroSmsProducts,
  refreshHeroSmsActivation,
  reorderHeroSmsActivation,
} from './api'
import { isHeroSmsActiveStatus } from './status-meta'
import type { HeroSmsActivation } from './types'

export const heroSmsQueryKeys = {
  all: ['hero-sms'] as const,
  products: (site?: string) => ['hero-sms', 'products', site || 'all'] as const,
  activations: (page: number, size: number, status?: string) =>
    ['hero-sms', 'activations', page, size, status || 'all'] as const,
  activation: (activationId: number | string) =>
    ['hero-sms', 'activation', String(activationId)] as const,
}

export function usePageVisibility() {
  const [isVisible, setIsVisible] = useState(
    typeof document === 'undefined' ? true : document.visibilityState === 'visible'
  )

  useEffect(() => {
    const handleVisibilityChange = () => {
      setIsVisible(document.visibilityState === 'visible')
    }

    document.addEventListener('visibilitychange', handleVisibilityChange)
    return () => {
      document.removeEventListener('visibilitychange', handleVisibilityChange)
    }
  }, [])

  return isVisible
}

export function getHeroSmsPollingInterval(
  items: HeroSmsActivation[] | undefined,
  pageVisible: boolean,
  enabled = true
) {
  if (!enabled || !items?.some((item) => isHeroSmsActiveStatus(item.status))) {
    return false
  }

  return pageVisible ? 5000 : 30000
}

export function useHeroSmsProducts(site?: string) {
  const normalizedSite = site?.trim() ?? ''

  return useQuery({
    queryKey: heroSmsQueryKeys.products(normalizedSite),
    queryFn: () =>
      listHeroSmsProducts({ page: 1, size: 100, site: normalizedSite }),
    enabled: normalizedSite.length > 0,
    staleTime: 20_000,
  })
}

export function useHeroSmsActivations(params: {
  page: number
  size: number
  status?: string
  pollEnabled?: boolean
}) {
  const isPageVisible = usePageVisibility()

  return useQuery({
    queryKey: heroSmsQueryKeys.activations(params.page, params.size, params.status),
    queryFn: () =>
      listHeroSmsActivations({
        page: params.page,
        size: params.size,
        status:
          params.status && params.status !== 'all' ? params.status : undefined,
      }),
    placeholderData: (previousData) => previousData,
    refetchInterval: (query) =>
      getHeroSmsPollingInterval(
        query.state.data?.items,
        isPageVisible,
        params.pollEnabled !== false
      ),
  })
}

export function useHeroSmsActivationDetail(
  activationId: number | string | null,
  enabled = true
) {
  return useQuery({
    queryKey: heroSmsQueryKeys.activation(activationId || 'none'),
    queryFn: () => getHeroSmsActivationDetail(String(activationId)),
    enabled: enabled && activationId != null,
    placeholderData: (previousData) => previousData,
  })
}

export function useCreateHeroSmsActivations() {
  return useMutation({ mutationFn: createHeroSmsActivations })
}

export function useRefreshHeroSmsActivation() {
  return useMutation({ mutationFn: refreshHeroSmsActivation })
}

export function useCancelHeroSmsActivation() {
  return useMutation({ mutationFn: cancelHeroSmsActivation })
}

export function useReorderHeroSmsActivation() {
  return useMutation({ mutationFn: reorderHeroSmsActivation })
}
