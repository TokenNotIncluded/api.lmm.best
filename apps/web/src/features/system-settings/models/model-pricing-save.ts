/*
Copyright (C) 2026 LIghtJUNction
*/
import type {
  ModelPricingConfig,
  updateModelPricingConfig,
} from './model-pricing-api'

export type ModelPricingSaveReceipt = Awaited<
  ReturnType<typeof updateModelPricingConfig>
>

export function modelPricingFormSnapshot(
  config: ModelPricingConfig,
  exposeRatioEnabled: boolean
) {
  const values = config.values
  return {
    ModelPrice: values.ModelPrice,
    ModelRatio: values.ModelRatio,
    CacheRatio: values.CacheRatio,
    CreateCacheRatio: values.CreateCacheRatio,
    CompletionRatio: values.CompletionRatio,
    ImageRatio: values.ImageRatio,
    AudioRatio: values.AudioRatio,
    AudioCompletionRatio: values.AudioCompletionRatio,
    BillingMode: values['billing_setting.billing_mode'],
    BillingExpr: values['billing_setting.billing_expr'],
    ExposeRatioEnabled: exposeRatioEnabled,
  }
}

// A committed receipt is authoritative even if a later read fails. Keep the
// read outside write-error handling so callers cannot report or retry a saved
// price as though its POST failed.
export async function acceptModelPricingSave(
  receipt: ModelPricingSaveReceipt,
  accept: (config: ModelPricingConfig) => void,
  refresh: () => Promise<void>
): Promise<{ refreshError?: unknown }> {
  accept(receipt.data)
  try {
    await refresh()
    return {}
  } catch (refreshError) {
    return { refreshError }
  }
}
