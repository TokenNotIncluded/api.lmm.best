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
/*
Copyright (C) 2026 LIghtJUNction
*/
import { useQueryClient } from '@tanstack/react-query'
import { useCallback, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { getSystemOptions, updateSystemOption } from '../api'
import { useSystemOptions } from '../hooks/use-system-options'
import {
  getModelPricingConfig,
  MODEL_PRICING_QUERY_KEY,
  useModelPricingConfig,
} from './model-pricing-api'
import { buildModelSnapshots } from './model-pricing-snapshots'

export function parseModelPriceLocks(value?: string): Record<string, boolean> {
  try {
    const parsed: unknown = JSON.parse(value || '{}')
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return {}
    }
    return Object.fromEntries(
      Object.entries(parsed).filter(([, locked]) => locked === true)
    )
  } catch {
    return {}
  }
}

export function useModelPriceLocks() {
  const { t } = useTranslation()
  const query = useSystemOptions()
  const pricingQuery = useModelPricingConfig()
  const queryClient = useQueryClient()
  const [pending, setPending] = useState(false)
  const pendingRef = useRef(false)
  const options = pricingQuery.data
  const canonicalOptions = useMemo(
    () =>
      options
        ? Object.entries(options.values).map(([key, value]) => ({ key, value }))
        : undefined,
    [options]
  )
  const locks = useMemo(
    () => parseModelPriceLocks(options?.values.ModelPriceLock),
    [options]
  )
  const snapshots = useMemo(() => {
    if (!options) return undefined
    const values = options.values
    return buildModelSnapshots({
      creditsPerUsd: options.credits_per_usd,
      modelPrice: values.ModelPrice || '{}',
      modelRatio: values.ModelRatio || '{}',
      cacheRatio: values.CacheRatio || '{}',
      createCacheRatio: values.CreateCacheRatio || '{}',
      completionRatio: values.CompletionRatio || '{}',
      imageRatio: values.ImageRatio || '{}',
      audioRatio: values.AudioRatio || '{}',
      audioCompletionRatio: values.AudioCompletionRatio || '{}',
      billingMode: values['billing_setting.billing_mode'] || '{}',
      billingExpr: values['billing_setting.billing_expr'] || '{}',
    })
  }, [options])

  const toggle = useCallback(
    async (name: string) => {
      if (pendingRef.current) return
      const locked = locks[name] !== true
      pendingRef.current = true
      setPending(true)
      let writeAttempted = false
      try {
        // Check every write: a cached capability can outlive a backend rollback.
        const current = await getSystemOptions()
        if (!current.success) {
          throw new Error(current.message || t('Failed to load settings'))
        }
        if (current.capabilities?.model_price_locks !== true) {
          toast.warning(
            t(
              'The server does not support price locks. Update the server before locking prices.'
            )
          )
          return
        }
        await getModelPricingConfig()
        await queryClient.cancelQueries({ queryKey: MODEL_PRICING_QUERY_KEY })
        writeAttempted = true
        const response = await updateSystemOption({
          key: 'ModelPriceLock',
          model: name,
          value: locked,
        })
        if (!response.success) {
          throw new Error(response.message || t('Failed to update price lock'))
        }
        // The legacy receipt contains storage-unit prices. Never merge it into
        // the USD editor: only a canonical post-write read can refresh it.
        const accepted = await getModelPricingConfig()
        if (
          (parseModelPriceLocks(accepted.values.ModelPriceLock)[name] ===
            true) !==
          locked
        ) {
          throw new Error(t('Failed to update price lock'))
        }
        await queryClient.cancelQueries({ queryKey: MODEL_PRICING_QUERY_KEY })
        queryClient.setQueryData(MODEL_PRICING_QUERY_KEY, accepted)
        void queryClient.invalidateQueries({ queryKey: ['system-options'] })
        if (response.warnings?.length) {
          response.warnings.forEach((warning) => toast.warning(warning))
        }
        return {
          locked,
          options: Object.entries(accepted.values).map(([key, value]) => ({
            key,
            value,
          })),
        }
      } catch (error) {
        if (writeAttempted) {
          // A transport/refresh failure does not prove the write was rejected.
          // Mark cached locks stale so a later successful read can reconcile.
          void queryClient.invalidateQueries({
            queryKey: MODEL_PRICING_QUERY_KEY,
          })
        }
        toast.error(
          error instanceof Error
            ? error.message
            : t('Failed to update price lock')
        )
      } finally {
        pendingRef.current = false
        setPending(false)
      }
    },
    [locks, queryClient, t]
  )

  return {
    locks,
    snapshots,
    options: canonicalOptions,
    pending:
      pending ||
      query.isLoading ||
      pricingQuery.isLoading ||
      pricingQuery.isError,
    toggle,
  }
}
