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
import { useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'

import { useStatus } from '@/hooks/use-status'

import { getPricing } from '../api'
import { useModelRuntime } from './use-model-runtime'

export function usePricingData(options?: { enabled?: boolean }) {
  const { status } = useStatus()

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ['pricing'],
    queryFn: ({ signal }) => getPricing(signal),
    enabled: options?.enabled ?? true,
    staleTime: 5 * 60 * 1000,
  })

  // Missing/invalid conversion rates must not fabricate a USD estimate.
  const priceRate = useMemo(
    () => Number(status?.price ?? Number.NaN),
    [status?.price]
  )
  const usdExchangeRate = useMemo(
    () => Number(status?.usd_exchange_rate ?? Number.NaN),
    [status?.usd_exchange_rate]
  )

  const models = useMemo(() => {
    if (!data?.data || !data?.vendors) return []

    const vendorMap = new Map(data.vendors.map((v) => [v.id, v]))

    return data.data.map((model) => {
      const vendor = model.vendor_id
        ? vendorMap.get(model.vendor_id)
        : undefined
      return {
        ...model,
        key: model.model_name,
        vendor_name: vendor?.name,
        vendor_icon: vendor?.icon,
        vendor_description: vendor?.description,
        group_ratio: data.group_ratio,
      }
    })
  }, [data])

  const runtime = useModelRuntime(
    models.map((model) => model.model_name),
    options?.enabled ?? true
  )
  const liveModels = useMemo(
    () =>
      models.map((model) => ({
        ...model,
        runtime_state: runtime[model.model_name],
      })),
    [models, runtime]
  )
  return {
    models: liveModels,
    vendors: data?.vendors ?? [],
    groupRatio: data?.group_ratio ?? {},
    usableGroup: data?.usable_group ?? {},
    endpointMap: data?.supported_endpoint ?? {},
    autoGroups: data?.auto_groups ?? [],
    isLoading,
    error,
    refetch,
    priceRate,
    usdExchangeRate,
  }
}
