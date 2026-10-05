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
import type { QuotaDataItem } from '@/features/dashboard/types'

export type AssistantUsageModelSummary = {
  model: string
  requests: number
  tokens: number
  credits: number
  sharePercent: number
}

export type AssistantUsageSummary = {
  requests: number
  tokens: number
  credits: number
  models: AssistantUsageModelSummary[]
}

function nonNegativeNumber(value: number | undefined): number {
  return Number.isFinite(value) && (value ?? 0) > 0 ? Number(value) : 0
}

export function summarizeAssistantUsage(
  rows: QuotaDataItem[]
): AssistantUsageSummary {
  const byModel = new Map<
    string,
    { requests: number; tokens: number; quota: number }
  >()
  let requests = 0
  let tokens = 0
  let quota = 0

  for (const row of rows) {
    const rowRequests = nonNegativeNumber(row.count)
    const rowTokens = nonNegativeNumber(row.token_used)
    const rowQuota = nonNegativeNumber(row.quota)
    requests += rowRequests
    tokens += rowTokens
    quota += rowQuota

    const model = row.model_name?.trim() ?? ''
    const current = byModel.get(model) ?? { requests: 0, tokens: 0, quota: 0 }
    current.requests += rowRequests
    current.tokens += rowTokens
    current.quota += rowQuota
    byModel.set(model, current)
  }

  const shareDenominator = quota || tokens || requests
  const models = [...byModel.entries()]
    .map(([model, value]) => {
      let shareNumerator = value.requests
      if (quota > 0) shareNumerator = value.quota
      else if (tokens > 0) shareNumerator = value.tokens
      return {
        model,
        requests: value.requests,
        tokens: value.tokens,
        credits: value.quota,
        sharePercent:
          shareDenominator > 0
            ? Math.min(100, (shareNumerator / shareDenominator) * 100)
            : 0,
      }
    })
    .filter(
      (model) => model.requests > 0 || model.tokens > 0 || model.credits > 0
    )
    .sort(
      (left, right) =>
        right.credits - left.credits ||
        right.tokens - left.tokens ||
        right.requests - left.requests ||
        left.model.localeCompare(right.model)
    )

  return {
    requests,
    tokens,
    credits: quota,
    models,
  }
}
