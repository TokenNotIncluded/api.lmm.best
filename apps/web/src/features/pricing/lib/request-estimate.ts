/* Copyright (C) 2026 LIghtJUNction. SPDX-License-Identifier: AGPL-3.0-or-later */
import type { PricingModel } from '../types'
import { evaluateTextRequestExpression } from './billing-expr'

/** Plain text only; cached tokens are a subset of total input. No rounding until display. */
export function estimateRequestCost(
  model: PricingModel,
  ratio: number | undefined,
  input: number,
  output: number,
  cached: number
): number | null {
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
  if (
    model.quota_type !== 0 ||
    ![model.model_ratio, model.completion_ratio].every(
      (n) => Number.isFinite(n) && n >= 0
    )
  ) {
    return null
  }
  const cacheRatio = model.cache_ratio
  if (
    cached > 0 &&
    (cacheRatio == null || !Number.isFinite(cacheRatio) || cacheRatio < 0)
  ) {
    return null
  }
  const amount =
    ((input -
      cached +
      cached * (cacheRatio ?? 0) +
      output * model.completion_ratio) *
      model.model_ratio *
      2 *
      ratio) /
    1_000_000
  return Number.isFinite(amount) ? amount : null
}
