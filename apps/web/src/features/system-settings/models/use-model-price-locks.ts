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
import type { SystemOptionsResponse } from '../types'
import {
  mergePriceLockSnapshot,
  resolvePriceLockSnapshot,
} from './model-price-lock-snapshot'
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
  const queryClient = useQueryClient()
  const [pending, setPending] = useState(false)
  const pendingRef = useRef(false)
  const options = query.data
  const locks = useMemo(
    () =>
      parseModelPriceLocks(
        options?.data?.find(({ key }) => key === 'ModelPriceLock')?.value
      ),
    [options]
  )
  const snapshots = useMemo(() => {
    if (!options?.success) return undefined
    const values = Object.fromEntries(
      options.data.map(({ key, value }) => [key, value])
    )
    return buildModelSnapshots({
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
        await queryClient.cancelQueries({ queryKey: ['system-options'] })
        writeAttempted = true
        const response = await updateSystemOption({
          key: 'ModelPriceLock',
          model: name,
          value: locked,
        })
        if (!response.success) {
          throw new Error(response.message || t('Failed to update price lock'))
        }
        const { pricing, legacyOptions } = await resolvePriceLockSnapshot(
          response,
          name,
          locked,
          getSystemOptions
        )
        // Cancel reads started during the write before publishing its receipt.
        await queryClient.cancelQueries({ queryKey: ['system-options'] })
        const previous = queryClient.getQueryData<SystemOptionsResponse>([
          'system-options',
        ])
        const accepted = mergePriceLockSnapshot(
          legacyOptions || (previous?.success ? previous : current),
          pricing
        )
        queryClient.setQueryData(['system-options'], accepted)
        if (response.warnings?.length) {
          response.warnings.forEach((warning) => toast.warning(warning))
        }
        return { locked, options: accepted.data }
      } catch (error) {
        if (writeAttempted) {
          // A transport/refresh failure does not prove the write was rejected.
          // Mark cached locks stale so a later successful read can reconcile.
          void queryClient.invalidateQueries({ queryKey: ['system-options'] })
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
    options: options?.data,
    pending: pending || query.isLoading,
    toggle,
  }
}
