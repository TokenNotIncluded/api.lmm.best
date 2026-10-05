/*
Copyright (C) 2026 LIghtJUNction
SPDX-License-Identifier: AGPL-3.0-or-later
*/
import type { TFunction } from 'i18next'

import type { BillingRule, MarketTool } from './api'
import { creditAmount } from './money'

export const usageMetrics = [
  {
    metric: 'input_tokens',
    label: 'Million input tokens',
    quantity: 'Input tokens',
    scale: 1000000,
    limitScale: 1,
  },
  {
    metric: 'output_tokens',
    label: 'Million output tokens',
    quantity: 'Output tokens',
    scale: 1000000,
    limitScale: 1,
  },
  {
    metric: 'input_characters',
    label: 'Thousand input characters',
    quantity: 'Input characters',
    scale: 1000,
    limitScale: 1,
  },
  {
    metric: 'output_characters',
    label: 'Thousand output characters',
    quantity: 'Output characters',
    scale: 1000,
    limitScale: 1,
  },
  {
    metric: 'images',
    label: 'Image',
    quantity: 'Images',
    scale: 1,
    limitScale: 1,
  },
  {
    metric: 'audio_milliseconds',
    label: 'Audio second',
    quantity: 'Audio seconds',
    scale: 1000,
    limitScale: 1000,
  },
  {
    metric: 'video_milliseconds',
    label: 'Video second',
    quantity: 'Video seconds',
    scale: 1000,
    limitScale: 1000,
  },
  {
    metric: 'cpu_core_milliseconds',
    label: 'CPU core-second',
    quantity: 'CPU core-seconds',
    scale: 1000,
    limitScale: 1000,
  },
  {
    metric: 'memory_mib_seconds',
    label: 'Memory GiB-second',
    quantity: 'Memory GiB-seconds',
    scale: 1024,
    limitScale: 1024,
  },
  {
    metric: 'gpu_milliseconds',
    label: 'GPU second',
    quantity: 'GPU seconds',
    scale: 1000,
    limitScale: 1000,
  },
  {
    metric: 'vm_milliseconds',
    label: 'VM second',
    quantity: 'VM seconds',
    scale: 1000,
    limitScale: 1000,
  },
  {
    metric: 'storage_mib_seconds',
    label: 'Storage GiB-hour',
    quantity: 'Storage GiB-hours',
    scale: 3686400,
    limitScale: 3686400,
  },
] as const

export function maximumUsageQuota(rules: BillingRule[]): number {
  if (!rules.length || rules.length > usageMetrics.length) {
    throw new Error('Invalid usage rules')
  }
  const seen = new Set<string>()
  let total = 0n
  for (const r of rules) {
    const metric = usageMetrics.find((m) => m.metric === r.metric)
    if (
      !metric ||
      seen.has(r.metric) ||
      !Number.isSafeInteger(r.rate_quota) ||
      r.rate_quota <= 0 ||
      !Number.isSafeInteger(r.max_quantity) ||
      r.max_quantity <= 0 ||
      r.max_quantity > 1e12
    ) {
      throw new Error('Invalid usage rules')
    }
    seen.add(r.metric)
    total +=
      (BigInt(r.rate_quota) * BigInt(r.max_quantity) +
        BigInt(metric.scale) -
        1n) /
      BigInt(metric.scale)
  }
  if (total > BigInt(Number.MAX_SAFE_INTEGER)) {
    throw new Error('Usage price exceeds limit')
  }
  return Number(total)
}
export function usagePriceLabel(
  tool: Pick<MarketTool, 'billing_rules'>,
  units: number,
  t: TFunction
): string {
  return (tool.billing_rules ?? [])
    .map((r) =>
      t('{{amount}} credits per {{unit}}', {
        amount: creditAmount(r.rate_quota, units),
        unit: t(
          usageMetrics.find((m) => m.metric === r.metric)?.label ?? r.metric
        ),
      })
    )
    .join(' + ')
}

export function usageQuantityLabel(
  quantities: Record<string, number>,
  t: TFunction
): string {
  return Object.entries(quantities)
    .map(([metric, quantity]) => {
      const unit = usageMetrics.find((m) => m.metric === metric)
      return `${quantity / (unit?.limitScale ?? 1)} ${t(unit?.quantity ?? metric)}`
    })
    .join(' + ')
}
