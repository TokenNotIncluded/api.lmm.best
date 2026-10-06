/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import { TOKEN_UNIT_DIVISORS } from '../constants'
import type { PriceType, PricingModel, TokenUnit } from '../types'
import { isDynamicPricingModel } from './dynamic-price'
import { getDisplayGroupRatio, isTokenBasedModel } from './model-helpers'
import { getTokenPriceUSD } from './price'
import { estimateRequestCost } from './request-estimate'

export const MAX_COMPARE_MODELS = 4

const DAYS_PER_MONTH = 30

export type CompareWorkload = {
  input: number
  output: number
  requestsPerDay: number
}

/** Adds or removes a model; a full tray ignores additions instead of evicting. */
export function toggleCompareSelection(
  current: readonly string[],
  modelName: string
): string[] {
  if (!modelName) return [...current]
  if (current.includes(modelName)) {
    return current.filter((name) => name !== modelName)
  }
  if (current.length >= MAX_COMPARE_MODELS) return [...current]
  return [...current, modelName]
}

/** Canonical base USD per token unit, matching the public model cards. */
export function getCompareTokenPrice(
  model: PricingModel,
  type: PriceType,
  tokenUnit: TokenUnit,
  selectedGroup?: string
): number | null {
  if (!isTokenBasedModel(model) || isDynamicPricingModel(model)) return null
  const price =
    (getTokenPriceUSD(model, type) *
      getDisplayGroupRatio(model, selectedGroup)) /
    TOKEN_UNIT_DIVISORS[tokenUnit]
  return Number.isFinite(price) && price >= 0 ? price : null
}

export function estimateWorkloadCost(
  model: PricingModel,
  workload: CompareWorkload,
  selectedGroup?: string
): { perRequest: number; perMonth: number } | null {
  const { input, output, requestsPerDay } = workload
  if (!Number.isFinite(requestsPerDay) || requestsPerDay < 0) return null
  const perRequest = estimateRequestCost(
    model,
    getDisplayGroupRatio(model, selectedGroup),
    input,
    output,
    0
  )
  if (perRequest === null) return null
  return {
    perRequest,
    perMonth: perRequest * requestsPerDay * DAYS_PER_MONTH,
  }
}

/**
 * Indexes holding the best value. Nothing is highlighted unless at least two
 * models report a comparable number and they actually differ.
 */
export function pickBestIndexes(
  values: ReadonlyArray<number | null | undefined>,
  direction: 'min' | 'max'
): Set<number> {
  const finite = values
    .map((value, index) => ({ value, index }))
    .filter(
      (entry): entry is { value: number; index: number } =>
        typeof entry.value === 'number' && Number.isFinite(entry.value)
    )
  if (finite.length < 2) return new Set()
  const numbers = finite.map((entry) => entry.value)
  const best = direction === 'min' ? Math.min(...numbers) : Math.max(...numbers)
  const worst =
    direction === 'min' ? Math.max(...numbers) : Math.min(...numbers)
  if (best === worst) return new Set()
  return new Set(
    finite.filter((entry) => entry.value === best).map((entry) => entry.index)
  )
}

/** Share saved by the cheapest option relative to the most expensive one. */
export function getSavingsPercent(
  values: ReadonlyArray<number | null | undefined>
): number | null {
  const finite = values.filter(
    (value): value is number =>
      typeof value === 'number' && Number.isFinite(value)
  )
  if (finite.length < 2) return null
  const max = Math.max(...finite)
  const min = Math.min(...finite)
  if (max <= 0 || min === max) return null
  return ((max - min) / max) * 100
}
