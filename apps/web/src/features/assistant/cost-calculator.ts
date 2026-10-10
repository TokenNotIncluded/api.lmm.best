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
import type { PricingModel } from '@/features/pricing/types'

type AssistantPricingModel = PricingModel & {
  input_price?: number | null
  output_price?: number | null
  pricing_schema_version?: number
  pricing_currency?: string
}

export type AssistantCostEstimate = {
  inputRatePerMillionUSD: number
  outputRatePerMillionUSD: number
  totalUSD: number
}

export function hasAssistantUSDTextRates(
  model: AssistantPricingModel
): boolean {
  return (
    model.quota_type === 0 &&
    model.billing_mode !== 'tiered_expr' &&
    model.pricing_schema_version === 2 &&
    model.pricing_currency === 'USD' &&
    typeof model.input_price === 'number' &&
    Number.isFinite(model.input_price) &&
    model.input_price >= 0 &&
    typeof model.output_price === 'number' &&
    Number.isFinite(model.output_price) &&
    model.output_price >= 0
  )
}

export function calculateAssistantTextCost(
  model: AssistantPricingModel,
  groupRatio: number,
  inputTokens: number,
  outputTokens: number
): AssistantCostEstimate | null {
  if (
    !hasAssistantUSDTextRates(model) ||
    !Number.isFinite(groupRatio) ||
    !Number.isFinite(inputTokens) ||
    !Number.isFinite(outputTokens) ||
    groupRatio < 0 ||
    inputTokens < 0 ||
    outputTokens < 0
  ) {
    return null
  }

  // Schema 2 publishes real USD rates; model_ratio stays in calibrated
  // credits per token for settlement and must never be treated as USD.
  const inputRateUSD = model.input_price
  const outputRateUSD = model.output_price
  if (
    typeof inputRateUSD !== 'number' ||
    typeof outputRateUSD !== 'number' ||
    !Number.isFinite(inputRateUSD) ||
    !Number.isFinite(outputRateUSD) ||
    inputRateUSD < 0 ||
    outputRateUSD < 0
  ) {
    return null
  }

  const inputRatePerMillionUSD = inputRateUSD * groupRatio
  const outputRatePerMillionUSD = outputRateUSD * groupRatio
  const totalUSD =
    (inputTokens / 1_000_000) * inputRatePerMillionUSD +
    (outputTokens / 1_000_000) * outputRatePerMillionUSD
  if (
    !Number.isFinite(inputRatePerMillionUSD) ||
    !Number.isFinite(outputRatePerMillionUSD) ||
    !Number.isFinite(totalUSD)
  ) {
    return null
  }
  return {
    inputRatePerMillionUSD,
    outputRatePerMillionUSD,
    totalUSD,
  }
}
