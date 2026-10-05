/*
Copyright (C) 2026 LIghtJUNction
*/
import { useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api'

export const MODEL_PRICING_QUERY_KEY = ['model-pricing-usd'] as const
export const USD_PRICING_KEYS = [
  'ModelRatio',
  'CompletionRatio',
  'ModelPrice',
  'CacheRatio',
  'CreateCacheRatio',
  'ImageRatio',
  'AudioRatio',
  'AudioCompletionRatio',
  'billing_setting.billing_mode',
  'billing_setting.billing_expr',
  'ModelPriceLock',
  'tool_price_setting.prices',
] as const

export type ModelPricingConfig = {
  schema_version: 2
  currency: 'USD'
  storage_basis: 'legacy_pricing_unit'
  revision: string
  credits_per_usd: number
  legacy_pricing_units_per_usd: number
  model_ratio_usd_per_million: number
  tool_price_defaults?: Record<string, number>
  values: Record<(typeof USD_PRICING_KEYS)[number], string>
}

export type ModelPricingResponse = {
  success: boolean
  message?: string
  data?: ModelPricingConfig
  warnings?: string[]
  locked_models?: string[]
}

export function acceptModelPricingResponse(response: ModelPricingResponse) {
  const config = response.data
  if (!response.success || !config) {
    throw new Error(response.message || 'Failed to load USD model prices')
  }
  if (
    config.schema_version !== 2 ||
    config.currency !== 'USD' ||
    config.storage_basis !== 'legacy_pricing_unit' ||
    !/^[a-f0-9]{64}$/i.test(config.revision) ||
    !Number.isFinite(config.credits_per_usd) ||
    config.credits_per_usd <= 0 ||
    !Number.isFinite(config.legacy_pricing_units_per_usd) ||
    config.legacy_pricing_units_per_usd <= 0 ||
    !Number.isFinite(config.model_ratio_usd_per_million) ||
    Math.abs(
      config.model_ratio_usd_per_million - 1_000_000 / config.credits_per_usd
    ) > 1e-12 ||
    !config.values ||
    USD_PRICING_KEYS.some((key) => typeof config.values[key] !== 'string')
  ) {
    throw new Error('The server did not return USD pricing schema version 2')
  }
  return config
}

export async function getModelPricingConfig(
  options: { silent?: boolean } = {}
) {
  const response = await api.get<ModelPricingResponse>(
    '/api/option/pricing',
    options.silent
      ? { skipBusinessError: true, skipErrorHandler: true }
      : undefined
  )
  return acceptModelPricingResponse(response.data)
}

export function useModelPricingConfig(enabled = true) {
  return useQuery({
    queryKey: MODEL_PRICING_QUERY_KEY,
    queryFn: () => getModelPricingConfig({ silent: true }),
    enabled,
    retry: false,
    staleTime: 0,
  })
}

export function buildUsdPricingRequest(
  config: ModelPricingConfig,
  values: Record<string, string>
) {
  if (
    Object.keys(values).some(
      (key) => !(USD_PRICING_KEYS as readonly string[]).includes(key)
    )
  ) {
    throw new Error('Unsupported USD pricing setting')
  }
  return {
    schema_version: 2 as const,
    currency: 'USD' as const,
    expected_revision: config.revision,
    values,
  }
}

export async function updateModelPricingConfig(
  config: ModelPricingConfig,
  values: Record<string, string>,
  validateOnly = false,
  options: { silent?: boolean } = {}
) {
  const response = await api.post<ModelPricingResponse>(
    `/api/option/pricing/${validateOnly ? 'validate' : 'bulk'}`,
    buildUsdPricingRequest(config, values),
    options.silent
      ? { skipBusinessError: true, skipErrorHandler: true }
      : undefined
  )
  const accepted = acceptModelPricingResponse(response.data)
  return {
    ...response.data,
    success: true,
    message: response.data.message || '',
    data: accepted,
  }
}
