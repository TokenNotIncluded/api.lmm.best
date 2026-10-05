/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import type { PricingModel } from '../types'
import { evaluateTextRequestExpression } from './billing-expr'
import { getTokenPriceUSD, hasCanonicalPricing } from './price'

/** Plain text only; cached tokens are a subset of total input. No rounding until display. */
export function estimateRequestCost(
  model: PricingModel,
  ratio: number | undefined,
  input: number,
  output: number,
  cached: number
): number | null {
  if (!hasCanonicalPricing(model)) return null
  if (ratio === undefined || !Number.isFinite(ratio) || ratio < 0) return null
  if (
    ![input, output, cached].every((n) => Number.isSafeInteger(n) && n >= 0) ||
    cached > input
  ) {
    return null
  }
  if (model.billing_mode === 'tiered_expr') {
    if (!model.billing_expr?.trim()) return null
    try {
      const amount =
        (evaluateTextRequestExpression(
          model.billing_expr,
          input,
          output,
          cached
        ) /
          1_000_000) *
        ratio
      return Number.isFinite(amount) && amount >= 0 ? amount : null
    } catch {
      return null
    }
  }
  if (
    (model.billing_mode && model.billing_mode !== 'ratio') ||
    model.billing_expr?.trim()
  ) {
    return null
  }
  if (model.quota_type === 1) {
    return typeof model.model_price === 'number' &&
      Number.isFinite(model.model_price * ratio) &&
      model.model_price >= 0
      ? model.model_price * ratio
      : null
  }
  if (model.quota_type !== 0) return null
  const unitCost = (count: number, type: 'input' | 'output' | 'cache') => {
    if (count === 0) return 0
    const price = getTokenPriceUSD(model, type)
    return Number.isFinite(price) ? count * price : Number.NaN
  }
  const amount =
    ((unitCost(input - cached, 'input') +
      unitCost(cached, 'cache') +
      unitCost(output, 'output')) *
      ratio) /
    1_000_000
  return Number.isFinite(amount) ? amount : null
}
